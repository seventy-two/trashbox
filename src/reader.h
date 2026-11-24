// The reading pane: a WebView2 child window showing the pages the engine
// writes. It's made the first time a message is opened and closed again
// after a while with nothing to show, so Edge's processes only run while
// you're reading.
//
// Locked down: no scripts, no dev tools or browser shortcuts, nothing
// fetched from outside https://trashbox.view/ unless remote content is
// allowed for the page, no navigation away from it (links open in the
// default browser instead).
#pragma once

#include <windows.h>
#include <wrl.h>

#include <functional>
#include <string>
#include <vector>

#include "WebView2.h"

namespace tb {

class Reader {
public:
    // viewDir is served as https://trashbox.view/; userDir holds WebView2's
    // own profile. wake is called when something needs a frame.
    Reader(HWND parent, std::wstring viewDir, std::wstring userDir, std::function<void()> wake);
    ~Reader();

    // Show loads url into the pane at r (client pixels of the parent).
    void Show(const std::string& url, bool remote, RECT r);
    // Place moves the pane; hidden hides it without unloading (e.g. while a
    // popup is open over where it sits).
    void Place(RECT r, bool hidden);
    // Clear blanks the pane (nothing selected).
    void Clear();
    // Tick closes the browser after a while unused.
    void Tick(bool windowVisible);

    bool Loading() const { return state_ == State::Creating || navigating_; }
    std::string Error() const { return error_; }
    // Keys pressed while the pane had focus that the app handles (Esc).
    std::vector<UINT> TakeKeys();
    // Focus gives keyboard focus back from the pane to the parent window.
    void GiveFocusBack();

private:
    enum class State { None, Creating, Ready, Failed };
    void create();
    void configure();
    void navigate();
    void close();

    HWND parent_;
    std::wstring viewDir_, userDir_;
    std::function<void()> wake_;
    State state_ = State::None;
    Microsoft::WRL::ComPtr<ICoreWebView2Environment> env_;
    Microsoft::WRL::ComPtr<ICoreWebView2Controller> ctl_;
    Microsoft::WRL::ComPtr<ICoreWebView2> wv_;
    std::wstring url_;       // the page wanted
    bool remote_ = false;    // remote content allowed on it
    bool navigating_ = false;
    bool shown_ = false;
    RECT rect_{};
    ULONGLONG lastUsed_ = 0;
    std::string error_;
    std::vector<UINT> keys_;
    int generation_ = 0;  // bumps on close, so late callbacks are ignored
};

}  // namespace tb
