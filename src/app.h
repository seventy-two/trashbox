// trashbox's window: accounts and folders on the left, the message list in
// the middle, the reading pane on the right. The mail itself lives in
// trashbox-engine; this side asks for pages of it and draws them.
#pragma once

#include <windows.h>

#include <cstdint>
#include <functional>
#include <map>
#include <memory>
#include <string>
#include <vector>

#include "engine.h"
#include "reader.h"
#include "theme.h"

namespace tb {

struct Folder {
    std::string id, name, parent, role;
    int unread = 0, total = 0;
};

struct Account {
    std::string id, kind, status, error;
    std::vector<Folder> folders;
    bool collapsed = false;
    const Folder* Inbox() const;
};

struct Msg {
    int64_t id = 0, date = 0;
    std::string account, folder, fromName, fromAddr, to, subject, snippet;
    bool seen = false, flagged = false, attach = false;
};

struct Addr {
    std::string name, addr;
};

struct Attachment {
    int index = 0;
    std::string name, type;
    int64_t size = 0;
};

struct Body {
    int64_t id = 0;
    bool loading = false;
    std::string error, subject, url;
    std::vector<Addr> from, to, cc;
    int64_t date = 0;
    bool hasRemote = false, remote = false;
    std::vector<Attachment> attachments;
    double shownAt = 0;  // when it appeared, for marking read
};

// View is what the middle column shows.
struct View {
    bool unified = true;
    std::string account, folder;
    bool operator==(const View& o) const {
        return unified == o.unified && (unified || (account == o.account && folder == o.folder));
    }
};

class App {
public:
    App(HWND hwnd, std::function<void()> wake);
    ~App();

    bool BeforeFrame();
    void Frame();
    void Pump();
    bool Animating() const;
    bool InCaption(float x, float y) const;
    void SetScale(float scale);
    bool Place(RECT& r) const;
    void SavePlace(const RECT& r);
    void ShowWindow();
    int Unread() const;  // unread in every inbox
    int New() const { return newCount_; }  // new mail since the window was last in front

private:
    void onEvent(const nlohmann::json& e);
    void request(nlohmann::json cmd);
    void loadList(bool more);
    void select(int64_t id);
    void openBody(int64_t id, bool remote);
    void markSeen(int64_t id, bool seen);
    bool inView(const std::string& account, const std::string& folder) const;
    std::string accountLabel(const std::string& id) const;
    int accountIndex(const std::string& id) const;
    void testStep();

    void drawTitle(float w);
    void drawSidebar(float x, float y, float w, float h);
    void drawList(float x, float y, float w, float h);
    void drawReading(float x, float y, float w, float h);
    bool iconButton(const char* glyph, const char* tip, float size = 30, bool danger = false);
    bool folderRow(const char* label, int unread, bool selected, float indent, bool strongCount);

    HWND hwnd_;
    std::function<void()> wake_;
    std::wstring dataDir_;
    bool demo_ = false;
    std::unique_ptr<Engine> engine_;
    std::unique_ptr<Reader> reader_;
    Fonts fonts_;
    float scale_ = 1;

    std::vector<Account> accounts_;
    View view_;
    std::vector<Msg> list_;
    bool listLoading_ = false, listMore_ = false, listStale_ = false;
    int64_t listReq_ = 0;     // the list reply we're waiting for
    int64_t nextReq_ = 1;
    bool scrollTop_ = false;  // a new view: back to the top
    int64_t selected_ = 0;
    bool scrollToSelected_ = false;
    Body body_;
    int64_t bodyReq_ = 0;
    RECT paneRect_{};
    bool paneHidden_ = true;

    int newCount_ = 0;
    std::string engineError_;
    std::string note_;  // a short message at the bottom of the list
    double noteUntil_ = 0;

    // Places in the title bar that aren't for dragging the window.
    std::vector<std::pair<ImVec2, ImVec2>> hot_;
    ImVec2 barMax_{0, 0};

    struct Config {
        bool placed = false;
        int x = 0, y = 0, w = 0, h = 0;
    } cfg_;
    void loadConfig();
    void saveConfig();

    // TRASHBOX_STEPS: a test script run one step at a time in demo mode.
    std::vector<std::string> steps_;
    size_t step_ = 0;
    double stepWait_ = 0;
};

}  // namespace tb
