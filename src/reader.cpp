#include "reader.h"

#include "WebView2EnvironmentOptions.h"
#include "util.h"

using Microsoft::WRL::Callback;
using Microsoft::WRL::ComPtr;

namespace tb {

namespace {

const wchar_t kHost[] = L"trashbox.view";
const wchar_t kOrigin[] = L"https://trashbox.view/";

bool ours(const wchar_t* uri) { return uri && _wcsnicmp(uri, kOrigin, wcslen(kOrigin)) == 0; }

// How long the browser stays up with nothing to show.
constexpr ULONGLONG kIdleClose = 3 * 60 * 1000;

std::wstring take(LPWSTR s) {
    std::wstring out = s ? s : L"";
    if (s) CoTaskMemFree(s);
    return out;
}

}  // namespace

Reader::Reader(HWND parent, std::wstring viewDir, std::wstring userDir, std::function<void()> wake)
    : parent_(parent), viewDir_(std::move(viewDir)), userDir_(std::move(userDir)), wake_(std::move(wake)) {}

Reader::~Reader() { close(); }

void Reader::Show(const std::string& url, bool remote, RECT r) {
    url_ = Widen(url);
    remote_ = remote;
    rect_ = r;
    shown_ = true;
    lastUsed_ = GetTickCount64();
    switch (state_) {
    case State::None:
    case State::Failed: create(); break;
    case State::Creating: break;  // navigates once it's ready
    case State::Ready: navigate(); break;
    }
}

void Reader::Place(RECT r, bool hidden) {
    rect_ = r;
    if (state_ != State::Ready) return;
    bool visible = shown_ && !hidden;
    BOOL was = FALSE;
    ctl_->get_IsVisible(&was);
    if (bool(was) != visible) ctl_->put_IsVisible(visible);
    if (visible) {
        RECT cur{};
        ctl_->get_Bounds(&cur);
        if (!EqualRect(&cur, &r)) ctl_->put_Bounds(r);
        lastUsed_ = GetTickCount64();
    }
}

void Reader::Clear() {
    shown_ = false;
    url_.clear();
    if (state_ == State::Ready) {
        ctl_->put_IsVisible(FALSE);
        wv_->Navigate(L"about:blank");
    }
}

void Reader::Tick(bool windowVisible) {
    if (state_ != State::Ready) return;
    if (shown_ && windowVisible) {
        lastUsed_ = GetTickCount64();
        return;
    }
    if (GetTickCount64() - lastUsed_ > kIdleClose) close();
}

std::vector<UINT> Reader::TakeKeys() {
    std::vector<UINT> out;
    out.swap(keys_);
    return out;
}

void Reader::GiveFocusBack() {
    if (GetFocus() != parent_) SetFocus(parent_);
}

void Reader::create() {
    state_ = State::Creating;
    error_.clear();
    int gen = ++generation_;
    auto opts = Microsoft::WRL::Make<CoreWebView2EnvironmentOptions>();
    // No sign-in prompts, sync, or spelling services for a mail viewer.
    opts->put_AdditionalBrowserArguments(L"--disable-features=msSmartScreenProtection,msEdgeIdentity --no-first-run");
    HRESULT hr = CreateCoreWebView2EnvironmentWithOptions(
        nullptr, userDir_.c_str(), opts.Get(),
        Callback<ICoreWebView2CreateCoreWebView2EnvironmentCompletedHandler>(
            [this, gen](HRESULT result, ICoreWebView2Environment* env) -> HRESULT {
                if (gen != generation_) return S_OK;
                if (FAILED(result) || !env) {
                    state_ = State::Failed;
                    error_ = "Couldn't start the Edge WebView2 runtime (0x" + [&] {
                        char b[16];
                        snprintf(b, sizeof b, "%08lX", (unsigned long)result);
                        return std::string(b);
                    }() + "). Is it installed?";
                    wake_();
                    return S_OK;
                }
                env_ = env;
                env_->CreateCoreWebView2Controller(
                    parent_, Callback<ICoreWebView2CreateCoreWebView2ControllerCompletedHandler>(
                                 [this, gen](HRESULT result, ICoreWebView2Controller* ctl) -> HRESULT {
                                     if (gen != generation_) return S_OK;
                                     if (FAILED(result) || !ctl) {
                                         state_ = State::Failed;
                                         error_ = "Couldn't create the reading pane.";
                                         wake_();
                                         return S_OK;
                                     }
                                     ctl_ = ctl;
                                     ctl_->get_CoreWebView2(&wv_);
                                     configure();
                                     state_ = State::Ready;
                                     if (shown_) navigate();
                                     wake_();
                                     return S_OK;
                                 })
                                 .Get());
                return S_OK;
            })
            .Get());
    if (FAILED(hr)) {
        state_ = State::Failed;
        error_ = "The Edge WebView2 runtime isn't installed.";
    }
}

void Reader::configure() {
    ComPtr<ICoreWebView2Settings> s;
    wv_->get_Settings(&s);
    s->put_IsScriptEnabled(FALSE);
    s->put_AreDefaultScriptDialogsEnabled(FALSE);
    s->put_IsWebMessageEnabled(FALSE);
    s->put_AreDevToolsEnabled(FALSE);
    s->put_IsStatusBarEnabled(TRUE);  // shows where a link really goes
    s->put_IsZoomControlEnabled(TRUE);
    s->put_AreDefaultContextMenusEnabled(TRUE);  // copy, copy link
    if (ComPtr<ICoreWebView2Settings3> s3; SUCCEEDED(s.As(&s3))) s3->put_AreBrowserAcceleratorKeysEnabled(FALSE);
    if (ComPtr<ICoreWebView2Settings4> s4; SUCCEEDED(s.As(&s4))) {
        s4->put_IsPasswordAutosaveEnabled(FALSE);
        s4->put_IsGeneralAutofillEnabled(FALSE);
    }

    if (ComPtr<ICoreWebView2_3> wv3; SUCCEEDED(wv_.As(&wv3)))
        wv3->SetVirtualHostNameToFolderMapping(kHost, viewDir_.c_str(), COREWEBVIEW2_HOST_RESOURCE_ACCESS_KIND_DENY);
    // Plain-text mail follows the app's dark look; HTML mail sets its own.
    if (ComPtr<ICoreWebView2_13> wv13; SUCCEEDED(wv_.As(&wv13))) {
        ComPtr<ICoreWebView2Profile> profile;
        if (SUCCEEDED(wv13->get_Profile(&profile))) profile->put_PreferredColorScheme(COREWEBVIEW2_PREFERRED_COLOR_SCHEME_DARK);
    }
    if (ComPtr<ICoreWebView2Controller2> c2; SUCCEEDED(ctl_.As(&c2))) c2->put_DefaultBackgroundColor({255, 0x15, 0x17, 0x1C});

    // Every request: ours and data: go through; the rest only when remote
    // content is allowed for this page.
    wv_->AddWebResourceRequestedFilter(L"*", COREWEBVIEW2_WEB_RESOURCE_CONTEXT_ALL);
    wv_->add_WebResourceRequested(
        Callback<ICoreWebView2WebResourceRequestedEventHandler>(
            [this](ICoreWebView2*, ICoreWebView2WebResourceRequestedEventArgs* args) -> HRESULT {
                ComPtr<ICoreWebView2WebResourceRequest> req;
                args->get_Request(&req);
                LPWSTR raw = nullptr;
                req->get_Uri(&raw);
                std::wstring uri = take(raw);
                bool data = _wcsnicmp(uri.c_str(), L"data:", 5) == 0;
                if (ours(uri.c_str()) || data || uri == L"about:blank") return S_OK;
                if (remote_ && (_wcsnicmp(uri.c_str(), L"https://", 8) == 0 || _wcsnicmp(uri.c_str(), L"http://", 7) == 0))
                    return S_OK;
                ComPtr<ICoreWebView2WebResourceResponse> resp;
                env_->CreateWebResourceResponse(nullptr, 403, L"Blocked by trashbox", L"", &resp);
                args->put_Response(resp.Get());
                return S_OK;
            })
            .Get(),
        nullptr);

    // Never leave our pages; a link the user clicked opens in the browser.
    wv_->add_NavigationStarting(Callback<ICoreWebView2NavigationStartingEventHandler>(
                                    [this](ICoreWebView2*, ICoreWebView2NavigationStartingEventArgs* args) -> HRESULT {
                                        LPWSTR raw = nullptr;
                                        args->get_Uri(&raw);
                                        std::wstring uri = take(raw);
                                        if (ours(uri.c_str()) || uri == L"about:blank") {
                                            navigating_ = true;
                                            return S_OK;
                                        }
                                        args->put_Cancel(TRUE);
                                        BOOL user = FALSE;
                                        args->get_IsUserInitiated(&user);
                                        if (user) OpenExternal(uri);
                                        return S_OK;
                                    })
                                    .Get(),
                                nullptr);
    wv_->add_NavigationCompleted(Callback<ICoreWebView2NavigationCompletedEventHandler>(
                                     [this](ICoreWebView2*, ICoreWebView2NavigationCompletedEventArgs*) -> HRESULT {
                                         navigating_ = false;
                                         wake_();
                                         return S_OK;
                                     })
                                     .Get(),
                                 nullptr);
    wv_->add_NewWindowRequested(Callback<ICoreWebView2NewWindowRequestedEventHandler>(
                                    [](ICoreWebView2*, ICoreWebView2NewWindowRequestedEventArgs* args) -> HRESULT {
                                        args->put_Handled(TRUE);  // no popup windows
                                        LPWSTR raw = nullptr;
                                        args->get_Uri(&raw);
                                        std::wstring uri = take(raw);
                                        BOOL user = FALSE;
                                        args->get_IsUserInitiated(&user);
                                        if (user) OpenExternal(uri);
                                        return S_OK;
                                    })
                                    .Get(),
                                nullptr);
    // Downloads (e.g. a link to a file) are refused; attachments are saved
    // by the app itself.
    if (ComPtr<ICoreWebView2_4> wv4; SUCCEEDED(wv_.As(&wv4)))
        wv4->add_DownloadStarting(Callback<ICoreWebView2DownloadStartingEventHandler>(
                                      [](ICoreWebView2*, ICoreWebView2DownloadStartingEventArgs* args) -> HRESULT {
                                          args->put_Cancel(TRUE);
                                          return S_OK;
                                      })
                                      .Get(),
                                  nullptr);
    // Esc from inside the pane goes to the app.
    ctl_->add_AcceleratorKeyPressed(
        Callback<ICoreWebView2AcceleratorKeyPressedEventHandler>(
            [this](ICoreWebView2Controller*, ICoreWebView2AcceleratorKeyPressedEventArgs* args) -> HRESULT {
                COREWEBVIEW2_KEY_EVENT_KIND kind;
                args->get_KeyEventKind(&kind);
                UINT key = 0;
                args->get_VirtualKey(&key);
                if (kind == COREWEBVIEW2_KEY_EVENT_KIND_KEY_DOWN && (key == VK_ESCAPE || key == VK_F5)) {
                    args->put_Handled(TRUE);
                    keys_.push_back(key);
                    wake_();
                }
                return S_OK;
            })
            .Get(),
        nullptr);
    ctl_->put_Bounds(rect_);
    ctl_->put_IsVisible(FALSE);
}

void Reader::navigate() {
    if (url_.empty()) return;
    navigating_ = true;
    // A query string makes a reload (e.g. with pictures allowed) fetch the
    // rewritten page rather than the cached one.
    std::wstring u = url_ + L"?v=" + std::to_wstring(GetTickCount64());
    wv_->Navigate(u.c_str());
    ctl_->put_Bounds(rect_);
    ctl_->put_IsVisible(TRUE);
}

void Reader::close() {
    generation_++;
    if (ctl_) ctl_->Close();
    wv_.Reset();
    ctl_.Reset();
    env_.Reset();
    state_ = State::None;
    navigating_ = false;
}

}  // namespace tb
