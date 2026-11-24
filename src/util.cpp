#include "util.h"

#include <shellapi.h>
#include <shlobj.h>
#include <shobjidl.h>
#include <wincodec.h>

#include <ctime>
#include <vector>

namespace tb {

std::wstring Widen(std::string_view s) {
    if (s.empty()) return {};
    int n = MultiByteToWideChar(CP_UTF8, 0, s.data(), int(s.size()), nullptr, 0);
    std::wstring w(size_t(n), L'\0');
    MultiByteToWideChar(CP_UTF8, 0, s.data(), int(s.size()), w.data(), n);
    return w;
}

std::string Narrow(std::wstring_view s) {
    if (s.empty()) return {};
    int n = WideCharToMultiByte(CP_UTF8, 0, s.data(), int(s.size()), nullptr, 0, nullptr, nullptr);
    std::string out(size_t(n), '\0');
    WideCharToMultiByte(CP_UTF8, 0, s.data(), int(s.size()), out.data(), n, nullptr, nullptr);
    return out;
}

static const char* kDays[] = {"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"};
static const char* kMonths[] = {"January", "February", "March",     "April",   "May",      "June",
                                "July",    "August",   "September", "October", "November", "December"};

static std::tm local(int64_t unix) {
    std::time_t t = std::time_t(unix);
    std::tm tm{};
    localtime_s(&tm, &t);
    return tm;
}

std::string ListDate(int64_t unix, int64_t now) {
    std::tm d = local(unix), n = local(now);
    char buf[32];
    bool sameDay = d.tm_year == n.tm_year && d.tm_yday == n.tm_yday;
    if (sameDay) {
        snprintf(buf, sizeof buf, "%02d:%02d", d.tm_hour, d.tm_min);
    } else if (now - unix < 6 * 86400 && unix <= now) {
        snprintf(buf, sizeof buf, "%.3s", kDays[d.tm_wday]);
    } else if (d.tm_year == n.tm_year) {
        snprintf(buf, sizeof buf, "%d %.3s", d.tm_mday, kMonths[d.tm_mon]);
    } else {
        snprintf(buf, sizeof buf, "%d %.3s %d", d.tm_mday, kMonths[d.tm_mon], d.tm_year + 1900);
    }
    return buf;
}

std::string TableDate(int64_t unix, int64_t now) {
    std::tm d = local(unix), n = local(now);
    char buf[32];
    if (d.tm_year == n.tm_year && d.tm_yday == n.tm_yday)
        snprintf(buf, sizeof buf, "%02d:%02d", d.tm_hour, d.tm_min);
    else if (d.tm_year == n.tm_year)
        snprintf(buf, sizeof buf, "%d %.3s %02d:%02d", d.tm_mday, kMonths[d.tm_mon], d.tm_hour, d.tm_min);
    else
        snprintf(buf, sizeof buf, "%d %.3s %d", d.tm_mday, kMonths[d.tm_mon], d.tm_year + 1900);
    return buf;
}

std::string LongDate(int64_t unix) {
    std::tm d = local(unix);
    char buf[64];
    snprintf(buf, sizeof buf, "%s %d %s %d, %02d:%02d", kDays[d.tm_wday], d.tm_mday, kMonths[d.tm_mon],
             d.tm_year + 1900, d.tm_hour, d.tm_min);
    return buf;
}

std::string Size(int64_t bytes) {
    char buf[32];
    if (bytes < 1024) snprintf(buf, sizeof buf, "%lld bytes", (long long)bytes);
    else if (bytes < 1024 * 1024) snprintf(buf, sizeof buf, "%lld KB", (long long)((bytes + 512) / 1024));
    else snprintf(buf, sizeof buf, "%.1f MB", bytes / (1024.0 * 1024.0));
    return buf;
}

bool OpenExternal(const std::wstring& url) {
    auto starts = [&](const wchar_t* p) { return _wcsnicmp(url.c_str(), p, wcslen(p)) == 0; };
    if (!starts(L"https://") && !starts(L"http://") && !starts(L"mailto:")) return false;
    return INT_PTR(ShellExecuteW(nullptr, L"open", url.c_str(), nullptr, nullptr, SW_SHOWNORMAL)) > 32;
}

void CopySecret(HWND owner, const std::string& text) {
    std::wstring w = Widen(text);
    if (!OpenClipboard(owner)) return;
    EmptyClipboard();
    if (HGLOBAL mem = GlobalAlloc(GMEM_MOVEABLE, (w.size() + 1) * sizeof(wchar_t))) {
        memcpy(GlobalLock(mem), w.c_str(), (w.size() + 1) * sizeof(wchar_t));
        GlobalUnlock(mem);
        if (!SetClipboardData(CF_UNICODETEXT, mem)) GlobalFree(mem);
    }
    if (UINT fmt = RegisterClipboardFormatW(L"ExcludeClipboardContentFromMonitorProcessing"))
        if (HGLOBAL flag = GlobalAlloc(GMEM_MOVEABLE, sizeof(DWORD))) {
            *static_cast<DWORD*>(GlobalLock(flag)) = 0;
            GlobalUnlock(flag);
            if (!SetClipboardData(fmt, flag)) GlobalFree(flag);
        }
    CloseClipboard();
}

bool SaveWindowPng(HWND hwnd, const std::wstring& path) {
    RECT r;
    GetClientRect(hwnd, &r);
    int w = r.right, h = r.bottom;
    HDC screen = GetDC(nullptr);
    HDC dc = CreateCompatibleDC(screen);
    BITMAPINFO bi{};
    bi.bmiHeader = {sizeof(BITMAPINFOHEADER), w, -h, 1, 32, BI_RGB};
    void* bits = nullptr;
    HBITMAP bmp = CreateDIBSection(screen, &bi, DIB_RGB_COLORS, &bits, nullptr, 0);
    HGDIOBJ old = SelectObject(dc, bmp);
    BOOL ok = PrintWindow(hwnd, dc, PW_CLIENTONLY | PW_RENDERFULLCONTENT);
    SelectObject(dc, old);
    DeleteDC(dc);
    ReleaseDC(nullptr, screen);
    if (!ok) {
        DeleteObject(bmp);
        return false;
    }
    auto* px = static_cast<unsigned char*>(bits);
    for (int i = 0; i < w * h; i++) px[i * 4 + 3] = 255;  // GDI leaves alpha at 0

    bool saved = false;
    IWICImagingFactory* f = nullptr;
    if (SUCCEEDED(CoCreateInstance(CLSID_WICImagingFactory, nullptr, CLSCTX_INPROC_SERVER, IID_PPV_ARGS(&f)))) {
        IWICStream* s = nullptr;
        IWICBitmapEncoder* enc = nullptr;
        IWICBitmapFrameEncode* frame = nullptr;
        if (SUCCEEDED(f->CreateStream(&s)) && SUCCEEDED(s->InitializeFromFilename(path.c_str(), GENERIC_WRITE)) &&
            SUCCEEDED(f->CreateEncoder(GUID_ContainerFormatPng, nullptr, &enc)) &&
            SUCCEEDED(enc->Initialize(s, WICBitmapEncoderNoCache)) && SUCCEEDED(enc->CreateNewFrame(&frame, nullptr)) &&
            SUCCEEDED(frame->Initialize(nullptr)) && SUCCEEDED(frame->SetSize(UINT(w), UINT(h)))) {
            WICPixelFormatGUID fmt = GUID_WICPixelFormat32bppBGRA;
            frame->SetPixelFormat(&fmt);
            saved = SUCCEEDED(frame->WritePixels(UINT(h), UINT(w * 4), UINT(w * h * 4), px)) &&
                    SUCCEEDED(frame->Commit()) && SUCCEEDED(enc->Commit());
        }
        if (frame) frame->Release();
        if (enc) enc->Release();
        if (s) s->Release();
        f->Release();
    }
    DeleteObject(bmp);
    return saved;
}

std::vector<std::wstring> PickFiles(HWND owner) {
    std::vector<std::wstring> out;
    IFileOpenDialog* d = nullptr;
    if (FAILED(CoCreateInstance(CLSID_FileOpenDialog, nullptr, CLSCTX_INPROC_SERVER, IID_PPV_ARGS(&d)))) return out;
    DWORD opts = 0;
    d->GetOptions(&opts);
    d->SetOptions(opts | FOS_ALLOWMULTISELECT | FOS_FILEMUSTEXIST | FOS_FORCEFILESYSTEM);
    d->SetTitle(L"Attach files");
    d->SetOkButtonLabel(L"Attach");
    IShellItemArray* items = nullptr;
    if (SUCCEEDED(d->Show(owner)) && SUCCEEDED(d->GetResults(&items))) {
        DWORD n = 0;
        items->GetCount(&n);
        for (DWORD i = 0; i < n; i++) {
            IShellItem* it = nullptr;
            PWSTR path = nullptr;
            if (SUCCEEDED(items->GetItemAt(i, &it)) && SUCCEEDED(it->GetDisplayName(SIGDN_FILESYSPATH, &path))) {
                out.push_back(path);
                CoTaskMemFree(path);
            }
            if (it) it->Release();
        }
        items->Release();
    }
    d->Release();
    return out;
}

std::wstring PickSavePath(HWND owner, const std::wstring& name) {
    std::wstring out;
    IFileSaveDialog* d = nullptr;
    if (FAILED(CoCreateInstance(CLSID_FileSaveDialog, nullptr, CLSCTX_INPROC_SERVER, IID_PPV_ARGS(&d)))) return out;
    DWORD opts = 0;
    d->GetOptions(&opts);
    d->SetOptions(opts | FOS_OVERWRITEPROMPT | FOS_FORCEFILESYSTEM);
    d->SetFileName(name.c_str());
    // Downloads to start with; after that Windows remembers where you went.
    IShellItem* dl = nullptr;
    if (SUCCEEDED(SHGetKnownFolderItem(FOLDERID_Downloads, KF_FLAG_DEFAULT, nullptr, IID_PPV_ARGS(&dl)))) {
        d->SetDefaultFolder(dl);
        dl->Release();
    }
    IShellItem* it = nullptr;
    if (SUCCEEDED(d->Show(owner)) && SUCCEEDED(d->GetResult(&it))) {
        PWSTR path = nullptr;
        if (SUCCEEDED(it->GetDisplayName(SIGDN_FILESYSPATH, &path))) {
            out = path;
            CoTaskMemFree(path);
        }
        it->Release();
    }
    d->Release();
    return out;
}

int64_t FileSize(const std::wstring& path) {
    WIN32_FILE_ATTRIBUTE_DATA a{};
    if (!GetFileAttributesExW(path.c_str(), GetFileExInfoStandard, &a)) return -1;
    if (a.dwFileAttributes & FILE_ATTRIBUTE_DIRECTORY) return -1;
    return (int64_t(a.nFileSizeHigh) << 32) | a.nFileSizeLow;
}

std::wstring BaseName(const std::wstring& path) {
    size_t i = path.find_last_of(L"\\/");
    return i == std::wstring::npos ? path : path.substr(i + 1);
}

}  // namespace tb
