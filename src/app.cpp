#include "app.h"

#include <shlobj.h>

#include <algorithm>
#include <cstdlib>
#include <ctime>
#include <fstream>
#include <sstream>

#include "imgui.h"
#include "imgui_internal.h"
#include "util.h"

using nlohmann::json;

namespace tb {

namespace icon {
constexpr const char* Minimise = "\xEE\xA4\xA1";   // U+E921
constexpr const char* Maximise = "\xEE\xA4\xA2";   // U+E922
constexpr const char* Restore = "\xEE\xA4\xA3";    // U+E923
constexpr const char* Close = "\xEE\xA2\xBB";      // U+E8BB
constexpr const char* Refresh = "\xEE\x9C\xAC";    // U+E72C
constexpr const char* Mail = "\xEE\x9C\x95";       // U+E715
constexpr const char* Send = "\xEE\x9C\xA4";       // U+E724
constexpr const char* Draft = "\xEE\x9C\x8F";      // U+E70F
constexpr const char* Archive = "\xEE\x9E\xB8";    // U+E7B8
constexpr const char* Junk = "\xEE\x9E\xBA";       // U+E7BA (Warning)
constexpr const char* Trash = "\xEE\x9D\x8D";      // U+E74D
constexpr const char* Folder = "\xEE\xA2\xB7";     // U+E8B7
constexpr const char* Flag = "\xEE\x9F\x81";       // U+E7C1
constexpr const char* Attach = "\xEE\x9C\xA3";     // U+E723
constexpr const char* All = "\xEE\xA3\xBD";        // U+E8FD
constexpr const char* Down = "\xEE\x9C\x8D";       // U+E70D
constexpr const char* Right = "\xEE\x9D\xAC";      // U+E76C
constexpr const char* Picture = "\xEE\xA2\xB9";    // U+E8B9
constexpr const char* Warning = "\xEE\x9E\xBA";    // U+E7BA
constexpr const char* Reply = "\xEE\xA3\x8A";      // U+E8CA MailReply
constexpr const char* ReplyAll = "\xEE\xA3\x82";   // U+E8C2 MailReplyAll
}  // namespace icon

namespace {

std::string str(const json& j, const char* k) {
    auto it = j.find(k);
    return it != j.end() && it->is_string() ? it->get<std::string>() : std::string();
}
int64_t num(const json& j, const char* k) {
    auto it = j.find(k);
    return it != j.end() && it->is_number() ? it->get<int64_t>() : 0;
}
bool flag(const json& j, const char* k) {
    auto it = j.find(k);
    return it != j.end() && it->is_boolean() && it->get<bool>();
}

std::wstring dataDir() {
    if (const wchar_t* d = _wgetenv(L"TRASHBOX_DATA"); d && *d) return d;
    PWSTR base = nullptr;
    std::wstring dir;
    if (SUCCEEDED(SHGetKnownFolderPath(FOLDERID_LocalAppData, 0, nullptr, &base))) dir = std::wstring(base) + L"\\trashbox";
    CoTaskMemFree(base);
    return dir;
}

std::wstring exeDir() {
    wchar_t p[MAX_PATH];
    GetModuleFileNameW(nullptr, p, MAX_PATH);
    std::wstring s = p;
    return s.substr(0, s.find_last_of(L'\\'));
}

// Each account's colour, in the unified list and the sidebar.
ImVec4 accountColour(int i) {
    static const unsigned c[] = {0x5AA8FF, 0x3FB950, 0xD9A23A, 0xA78BFA, 0xF778BA, 0x4FD1C5};
    return Hex(c[(i < 0 ? 0 : i) % 6]);
}

int roleRank(const std::string& role) {
    static const char* order[] = {"inbox", "drafts", "sent", "archive", "all", "flagged", "junk", "trash"};
    for (int i = 0; i < 8; i++)
        if (role == order[i]) return i;
    return 100;
}

const char* roleIcon(const std::string& role) {
    if (role == "inbox") return icon::Mail;
    if (role == "sent") return icon::Send;
    if (role == "drafts") return icon::Draft;
    if (role == "archive") return icon::Archive;
    if (role == "junk") return icon::Junk;
    if (role == "trash") return icon::Trash;
    if (role == "flagged") return icon::Flag;
    if (role == "all") return icon::All;
    return icon::Folder;
}

// ellipsis draws text clipped to maxX with "…" when it doesn't fit.
void ellipsis(ImDrawList* dl, ImVec2 pos, float maxX, const std::string& text, ImU32 col) {
    if (text.empty() || maxX <= pos.x) return;
    ImGui::PushStyleColor(ImGuiCol_Text, col);
    float h = ImGui::GetFontSize();
    ImGui::RenderTextEllipsis(dl, pos, ImVec2(maxX, pos.y + h + 2), maxX, text.c_str(), text.c_str() + text.size(), nullptr);
    ImGui::PopStyleColor();
}

std::string oneLine(std::string s) {
    for (char& c : s)
        if (c == '\n' || c == '\r' || c == '\t') c = ' ';
    return s;
}

std::string displayAddr(const Addr& a) { return a.name.empty() ? a.addr : a.name; }

std::string addrList(const std::vector<Addr>& v) {
    std::string out;
    for (const auto& a : v) {
        if (!out.empty()) out += ", ";
        out += displayAddr(a);
    }
    return out;
}

// InputText on a std::string. toStart, if set, puts the cursor at the very
// start once (a reply's quote sits below where you type).
struct TextCb {
    std::string* s;
    bool* toStart;
};

int textCallback(ImGuiInputTextCallbackData* d) {
    auto* cb = static_cast<TextCb*>(d->UserData);
    if (d->EventFlag == ImGuiInputTextFlags_CallbackResize) {
        cb->s->resize(size_t(d->BufTextLen));
        d->Buf = cb->s->data();
    } else if (d->EventFlag == ImGuiInputTextFlags_CallbackAlways && cb->toStart && *cb->toStart) {
        d->CursorPos = d->SelectionStart = d->SelectionEnd = 0;
        *cb->toStart = false;
    }
    return 0;
}

bool inputString(const char* id, std::string& s, ImVec2 size, bool multiline, ImGuiInputTextFlags flags = 0,
                 bool* toStart = nullptr) {
    TextCb cb{&s, toStart};
    flags |= ImGuiInputTextFlags_CallbackResize | (toStart ? ImGuiInputTextFlags_CallbackAlways : 0);
    if (multiline)
        return ImGui::InputTextMultiline(id, s.data(), s.capacity() + 1, size, flags, textCallback, &cb);
    ImGui::SetNextItemWidth(size.x);
    return ImGui::InputText(id, s.data(), s.capacity() + 1, flags, textCallback, &cb);
}

std::vector<std::string> strings(const json& j) {
    std::vector<std::string> out;
    if (j.is_array())
        for (const auto& v : j)
            if (v.is_string()) out.push_back(v.get<std::string>());
    return out;
}

}  // namespace

const Folder* Account::Inbox() const {
    for (const auto& f : folders)
        if (f.role == "inbox") return &f;
    return nullptr;
}

// sortAccounts puts the accounts in the saved order; new ones go last.
void App::sortAccounts() {
    auto rank = [&](const std::string& id) {
        auto it = std::find(cfg_.order.begin(), cfg_.order.end(), id);
        return it == cfg_.order.end() ? 1000 : int(it - cfg_.order.begin());
    };
    std::stable_sort(accounts_.begin(), accounts_.end(),
                     [&](const Account& a, const Account& b) { return rank(a.id) < rank(b.id); });
}

void App::moveAccount(size_t i, int d) {
    size_t j = size_t(int(i) + d);
    if (j >= accounts_.size()) return;
    std::swap(accounts_[i], accounts_[j]);
    cfg_.order.clear();
    for (const auto& a : accounts_) cfg_.order.push_back(a.id);
    saveConfig();
    listStale_ = true;  // the unified list's account colours follow the order
}

bool App::folderShown(const Account& a, const Folder& f) const {
    auto it = cfg_.shown.find(a.id);
    if (it == cfg_.shown.end()) return f.role == "inbox" || f.role == "junk";
    return it->second.count(f.id) > 0;
}

void App::setFolderShown(const Account& a, const Folder& f, bool on) {
    if (!cfg_.shown.count(a.id))  // start from the defaults
        for (const auto& x : a.folders)
            if (folderShown(a, x)) cfg_.shown[a.id].insert(x.id);
    if (on) cfg_.shown[a.id].insert(f.id);
    else cfg_.shown[a.id].erase(f.id);
    saveConfig();
}

App::App(HWND hwnd, std::function<void()> wake) : hwnd_(hwnd), wake_(std::move(wake)) {
    dataDir_ = dataDir();
    CreateDirectoryW(dataDir_.c_str(), nullptr);
    CreateDirectoryW((dataDir_ + L"\\view").c_str(), nullptr);
    if (const char* d = std::getenv("TRASHBOX_DEMO"); d && *d == '1') demo_ = true;
    if (const char* s = std::getenv("TRASHBOX_STEPS"); s && demo_) {
        std::stringstream ss(s);
        std::string part;
        while (std::getline(ss, part, ';'))
            if (!part.empty()) steps_.push_back(part);
    }
    loadConfig();
    loadDraft();
    fonts_ = LoadFonts();
    engine_ = std::make_unique<Engine>(exeDir() + L"\\trashbox-engine.exe", demo_ ? L"-demo" : L"", wake_);
    engine_->Start();
    reader_ = std::make_unique<Reader>(hwnd_, dataDir_ + L"\\view", dataDir_ + L"\\webview", wake_);
    request({{"t", "hello"}});
    loadList(false);
}

App::~App() {
    saveDraft(true);
    if (engine_) engine_->Send({{"t", "quit"}});
    reader_.reset();
    engine_.reset();
}

// ---------------------------------------------------------------- config

void App::loadConfig() {
    std::ifstream f(dataDir_ + L"\\window.json");
    if (!f) return;
    try {
        json j = json::parse(f);
        cfg_.placed = flag(j, "placed");
        cfg_.x = int(num(j, "x"));
        cfg_.y = int(num(j, "y"));
        cfg_.w = int(num(j, "w"));
        cfg_.h = int(num(j, "h"));
        if (auto it = j.find("listShare"); it != j.end() && it->is_number())
            cfg_.listShare = std::clamp(it->get<float>(), 0.1f, 0.9f);
        if (auto it = j.find("order"); it != j.end() && it->is_array())
            for (const auto& a : *it)
                if (a.is_string()) cfg_.order.push_back(a.get<std::string>());
        if (auto it = j.find("shown"); it != j.end() && it->is_object())
            for (auto& [acc, ids] : it->items())
                if (ids.is_array())
                    for (const auto& id : ids)
                        if (id.is_string()) cfg_.shown[acc].insert(id.get<std::string>());
    } catch (...) {
    }
}

void App::saveConfig() {
    if (demo_) return;
    json j = {{"placed", cfg_.placed}, {"x", cfg_.x}, {"y", cfg_.y}, {"w", cfg_.w}, {"h", cfg_.h}, {"listShare", cfg_.listShare}, {"order", cfg_.order}};
    json shown = json::object();
    for (const auto& [acc, ids] : cfg_.shown) shown[acc] = std::vector<std::string>(ids.begin(), ids.end());
    j["shown"] = shown;
    std::ofstream(dataDir_ + L"\\window.json") << j.dump(2);
}

bool App::Place(RECT& r) const {
    if (!cfg_.placed || cfg_.w < 200 || cfg_.h < 200) return false;
    r = RECT{cfg_.x, cfg_.y, cfg_.x + cfg_.w, cfg_.y + cfg_.h};
    return true;
}

void App::SavePlace(const RECT& r) {
    cfg_.placed = true;
    cfg_.x = r.left;
    cfg_.y = r.top;
    cfg_.w = r.right - r.left;
    cfg_.h = r.bottom - r.top;
    saveConfig();
}

void App::ShowWindow() {
    ::ShowWindow(hwnd_, IsIconic(hwnd_) ? SW_RESTORE : SW_SHOW);
    SetForegroundWindow(hwnd_);
}

int App::Unread() const {
    int n = 0;
    for (const auto& a : accounts_)
        if (const Folder* in = a.Inbox()) n += in->unread;
    return n;
}

void App::SetScale(float scale) {
    scale_ = scale;
    ApplyTheme(true, scale_);
}

bool App::InCaption(float x, float y) const {
    if (x >= barMax_.x || y >= barMax_.y) return false;
    for (const auto& [a, b] : hot_)
        if (x >= a.x && y >= a.y && x < b.x && y < b.y) return false;
    return true;
}

bool App::Animating() const {
    return listLoading_ || body_.loading || draft_.loading || draft_.sending || reader_->Loading() || (!steps_.empty() && step_ < steps_.size()) ||
           (!note_.empty() && ImGui::GetTime() < noteUntil_);
}

bool App::BeforeFrame() { return LoadNeededFonts(fonts_); }

// ---------------------------------------------------------------- engine

void App::request(json cmd) {
    if (engine_) engine_->Send(cmd);
}

void App::loadList(bool more) {
    json cmd = {{"t", "list"}, {"view", view_.unified ? "unified" : "folder"}, {"req", nextReq_}};
    if (!view_.unified) {
        cmd["account"] = view_.account;
        cmd["folder"] = view_.folder;
    }
    if (more && !list_.empty()) {
        cmd["beforeDate"] = list_.back().date;
        cmd["beforeId"] = list_.back().id;
        cmd["limit"] = 100;
    } else {
        // A refresh keeps as many rows as are loaded, so the list doesn't
        // shrink under the scroll position.
        cmd["limit"] = std::clamp<int>(int(list_.size()), 100, 500);
    }
    listReq_ = nextReq_++;
    listLoading_ = true;
    listStale_ = false;
    request(cmd);
}

void App::select(int64_t id) {
    if (id && draft_.shown) draft_.shown = false;  // kept: the title bar's pen brings it back
    if (id == selected_) return;
    selected_ = id;
    scrollToSelected_ = true;
    if (id) openBody(id, false);
    else {
        body_ = Body();
        reader_->Clear();
    }
}

void App::openBody(int64_t id, bool remote) {
    bool same = body_.id == id;
    if (!same) body_ = Body();
    body_.id = id;
    body_.loading = true;
    body_.error.clear();
    bodyReq_ = nextReq_++;
    request({{"t", "open"}, {"id", id}, {"remote", remote}, {"req", bodyReq_}});
}

void App::markSeen(int64_t id, bool seen) {
    for (auto& m : list_)
        if (m.id == id) m.seen = seen;
    request({{"t", "seen"}, {"ids", {id}}, {"seen", seen}});
}

// ---------------------------------------------------------------- writing

std::string App::defaultFrom() const {
    if (!view_.unified) return view_.account;
    if (body_.id)
        for (const auto& m : list_)
            if (m.id == body_.id) return m.account;
    return accounts_.empty() ? std::string() : accounts_[0].id;
}

// startDraft refuses to replace a draft with something typed in it: that
// one comes back instead, to be sent or thrown away first.
static bool keepExisting(Draft& d, std::string& note, double& noteUntil) {
    if (!d.open || !d.edited) return false;
    d.shown = true;
    note = "Send or discard this draft before starting another.";
    noteUntil = ImGui::GetTime() + 6;
    return true;
}

// freshDraft starts over. A field being typed in keeps its own copy of the
// text, so it's let go first or the old text would come back.
static void freshDraft(Draft& d) {
    d = Draft();
    ImGui::ClearActiveID();
}

void App::newMessage() {
    if (keepExisting(draft_, note_, noteUntil_)) return;
    if (draft_.open && !draft_.edited && draft_.kind == "New message") {
        draft_.shown = true;
        return;
    }
    freshDraft(draft_);
    draft_.open = draft_.shown = true;
    draft_.kind = "New message";
    draft_.from = defaultFrom();
    draft_.focus = 1;
}

void App::reply(bool all) {
    if (!body_.id || body_.loading || !body_.error.empty()) return;
    if (keepExisting(draft_, note_, noteUntil_)) return;
    freshDraft(draft_);
    draft_.open = draft_.shown = true;
    draft_.loading = true;
    draft_.kind = all ? "Reply all" : "Reply";
    draft_.subject = body_.subject;
    draftReq_ = nextReq_++;
    request({{"t", "draft"}, {"id", body_.id}, {"all", all}, {"req", draftReq_}});
}

void App::sendDraft() {
    if (draft_.sending || draft_.loading) return;
    draft_.error.clear();
    draft_.sending = true;
    sendReq_ = nextReq_++;
    json d = {{"from", draft_.from},           {"to", draft_.to},     {"cc", draft_.cc},
              {"bcc", draft_.showBcc ? draft_.bcc : ""}, {"subject", draft_.subject}, {"body", draft_.body},
              {"inReplyTo", draft_.inReplyTo}, {"references", draft_.references},
              {"attachments", draft_.attachments}};
    request({{"t", "send"}, {"draft", d}, {"req", sendReq_}});
}

void App::discardDraft() {
    freshDraft(draft_);
    draftReq_ = sendReq_ = 0;
    DeleteFileW((dataDir_ + L"\\draft.json").c_str());
}

// saveDraft keeps the draft on disk, so a crash or a restart doesn't lose
// it. While typing it's saved a moment after the last change.
void App::saveDraft(bool now) {
    if (demo_ || !draft_.dirty) return;
    if (!now && ImGui::GetTime() - draft_.changedAt < 1.5) return;
    draft_.dirty = false;
    std::wstring path = dataDir_ + L"\\draft.json";
    if (!draft_.open || !draft_.edited) {
        DeleteFileW(path.c_str());
        return;
    }
    json j = {{"kind", draft_.kind},       {"from", draft_.from},       {"to", draft_.to},
              {"cc", draft_.cc},           {"bcc", draft_.bcc},         {"showBcc", draft_.showBcc},
              {"subject", draft_.subject}, {"body", draft_.body},       {"inReplyTo", draft_.inReplyTo},
              {"references", draft_.references}, {"attachments", draft_.attachments}};
    std::wstring tmp = path + L".tmp";
    {
        std::ofstream f(tmp, std::ios::binary);
        f << j.dump(2);
        if (!f) return;
    }
    MoveFileExW(tmp.c_str(), path.c_str(), MOVEFILE_REPLACE_EXISTING);
}

void App::loadDraft() {
    if (demo_) return;
    std::ifstream f(dataDir_ + L"\\draft.json", std::ios::binary);
    if (!f) return;
    try {
        json j = json::parse(f);
        freshDraft(draft_);
        draft_.open = draft_.edited = true;
        draft_.kind = str(j, "kind");
        draft_.from = str(j, "from");
        draft_.to = str(j, "to");
        draft_.cc = str(j, "cc");
        draft_.bcc = str(j, "bcc");
        draft_.showBcc = flag(j, "showBcc");
        draft_.subject = str(j, "subject");
        draft_.body = str(j, "body");
        draft_.inReplyTo = str(j, "inReplyTo");
        draft_.references = strings(j["references"]);
        draft_.attachments = strings(j["attachments"]);
        if (draft_.kind.empty()) draft_.kind = "New message";
    } catch (...) {
    }
}

void App::attach(const std::vector<std::wstring>& paths) {
    for (const auto& w : paths) {
        if (FileSize(w) < 0) {
            note_ = "Can't attach " + Narrow(BaseName(w)) + ": folders and missing files can't go.";
            noteUntil_ = ImGui::GetTime() + 6;
            continue;
        }
        std::string p = Narrow(w);
        if (std::find(draft_.attachments.begin(), draft_.attachments.end(), p) == draft_.attachments.end())
            draft_.attachments.push_back(p);
    }
    draft_.edited = draft_.dirty = true;
    draft_.changedAt = ImGui::GetTime();
    draft_.error.clear();
}

void App::DropFiles(const std::vector<std::wstring>& paths) {
    if (paths.empty()) return;
    if (!draft_.open) newMessage();
    draft_.shown = true;
    attach(paths);
}

void App::OpenMessage(int64_t id) {
    if (!id) return;
    if (!demo_) ShowWindow();  // test runs never take the foreground
    select(0);  // so the same message opens afresh
    select(id);
}

bool App::inView(const std::string& account, const std::string& folder) const {
    if (view_.unified) {
        for (const auto& a : accounts_)
            if (a.id == account)
                if (const Folder* in = a.Inbox()) return in->id == folder;
        return false;
    }
    return view_.account == account && view_.folder == folder;
}

int App::accountIndex(const std::string& id) const {
    for (size_t i = 0; i < accounts_.size(); i++)
        if (accounts_[i].id == id) return int(i);
    return 0;
}

std::string App::accountLabel(const std::string& id) const { return id; }

static Msg parseMsg(const json& j) {
    Msg m;
    m.id = num(j, "id");
    m.date = num(j, "date");
    m.account = str(j, "account");
    m.folder = str(j, "folder");
    m.fromName = str(j, "fromName");
    m.fromAddr = str(j, "fromAddr");
    m.to = str(j, "to");
    m.subject = str(j, "subject");
    m.snippet = str(j, "snippet");
    m.seen = flag(j, "seen");
    m.flagged = flag(j, "flagged");
    m.attach = flag(j, "attach");
    return m;
}

static std::vector<Addr> parseAddrs(const json& j) {
    std::vector<Addr> out;
    if (!j.is_array()) return out;
    for (const auto& a : j) out.push_back({str(a, "name"), str(a, "addr")});
    return out;
}

void App::onEvent(const json& e) {
    std::string t = str(e, "t");
    int64_t req = num(e, "req");
    if (t == "hello") {
        std::vector<Account> next;
        for (const auto& a : e["accounts"]) {
            Account acc;
            acc.id = str(a, "id");
            acc.kind = str(a, "kind");
            acc.status = str(a, "status");
            acc.error = str(a, "error");
            for (const auto& old : accounts_)
                if (old.id == acc.id) {
                    acc.folders = old.folders;
                    acc.collapsed = old.collapsed;
                }
            next.push_back(std::move(acc));
            request({{"t", "folders"}, {"account", next.back().id}});
        }
        accounts_ = std::move(next);
        sortAccounts();
    } else if (t == "account") {
        const json& a = e["account"];
        for (auto& acc : accounts_)
            if (acc.id == str(a, "id")) {
                acc.status = str(a, "status");
                acc.error = str(a, "error");
            }
    } else if (t == "folders") {
        for (auto& acc : accounts_)
            if (acc.id == str(e, "account")) {
                acc.folders.clear();
                for (const auto& f : e["folders"])
                    acc.folders.push_back({str(f, "id"), str(f, "name"), str(f, "parent"), str(f, "role"),
                                           int(num(f, "unread")), int(num(f, "total"))});
            }
        // The unified view depends on knowing each inbox.
        if (view_.unified && list_.empty() && !listLoading_) listStale_ = true;
    } else if (t == "changed") {
        if (inView(str(e, "account"), str(e, "folder"))) listStale_ = true;
    } else if (t == "list") {
        if (req != listReq_) return;
        listLoading_ = false;
        std::vector<Msg> got;
        for (const auto& m : e["messages"]) got.push_back(parseMsg(m));
        if (num(e, "beforeId") == 0) {
            list_ = std::move(got);
        } else {
            for (auto& m : got)
                if (std::none_of(list_.begin(), list_.end(), [&](const Msg& x) { return x.id == m.id; }))
                    list_.push_back(std::move(m));
        }
        listMore_ = flag(e, "more");
    } else if (t == "body") {
        if (req != bodyReq_) return;
        body_.loading = false;
        body_.subject = str(e, "subject");
        body_.from = parseAddrs(e["from"]);
        body_.to = parseAddrs(e["to"]);
        body_.cc = parseAddrs(e["cc"]);
        body_.date = num(e, "date");
        const json& page = e["page"];
        body_.url = str(page, "url");
        body_.hasRemote = flag(page, "hasRemote");
        body_.remote = flag(page, "remote");
        body_.attachments.clear();
        for (const auto& a : e["attachments"])
            body_.attachments.push_back({int(num(a, "index")), str(a, "name"), str(a, "type"), num(a, "size")});
        // Opening a message reads it, as in Thunderbird. Marked by its ID, so
        // it works even if the list has been reloaded or paged since.
        if (!body_.markedRead) {
            body_.markedRead = true;
            const Msg* m = nullptr;
            for (const auto& x : list_)
                if (x.id == body_.id) m = &x;
            if (!m || !m->seen) markSeen(body_.id, true);
        }
        reader_->Show(body_.url, body_.remote, paneRect_);
    } else if (t == "draft") {
        if (req != draftReq_ || !draft_.loading) return;
        const json& d = e["draft"];
        draft_.loading = false;
        draft_.from = str(d, "from");
        draft_.to = str(d, "to");
        draft_.cc = str(d, "cc");
        draft_.subject = str(d, "subject");
        draft_.body = str(d, "body");
        draft_.inReplyTo = str(d, "inReplyTo");
        draft_.references = strings(d["references"]);
        draft_.focus = draft_.to.empty() ? 1 : 2;
        draft_.bodyToStart = true;
    } else if (t == "sent") {
        if (req != sendReq_) return;
        std::string to = draft_.to.empty() ? draft_.cc : draft_.to;
        discardDraft();
        note_ = "Sent to " + to;
        noteUntil_ = ImGui::GetTime() + 6;
    } else if (t == "saved") {
        std::wstring path = Widen(str(e, "path"));
        std::wstring dir = path.substr(0, path.size() - BaseName(path).size());
        note_ = "Saved " + Narrow(BaseName(path)) + " in " + Narrow(dir);
        noteUntil_ = ImGui::GetTime() + 6;
    } else if (t == "code") {
        code_ = {str(e, "from"), str(e, "code"), ImGui::GetTime() + 600};
    } else if (t == "newmail") {
        if (GetForegroundWindow() != hwnd_) newCount_++;
        note_ = "New: " + str(e, "from") + " \xE2\x80\x94 " + str(e, "subject");
        noteUntil_ = ImGui::GetTime() + 6;
    } else if (t == "error") {
        if (req && req == listReq_) listLoading_ = false;
        if (req && (req == draftReq_ || req == sendReq_)) {
            if (req == draftReq_ && draft_.loading) {
                // No reply to write from: fall back to an empty one.
                draft_.loading = false;
                draft_.from = defaultFrom();
                draft_.focus = 1;
            }
            draft_.sending = false;
            draft_.error = str(e, "error");
            if (!draft_.error.empty() && draft_.error[0] >= 'a' && draft_.error[0] <= 'z') draft_.error[0] -= 'a' - 'A';
        } else if (req && req == bodyReq_) {
            body_.loading = false;
            body_.error = str(e, "error");
            reader_->Clear();
        } else {
            note_ = str(e, "error");
            if (!note_.empty() && note_[0] >= 'a' && note_[0] <= 'z') note_[0] -= 'a' - 'A';
            noteUntil_ = ImGui::GetTime() + 8;
        }
    } else if (t == "stopped") {
        engineError_ = engine_ ? engine_->Error() : "the engine stopped";
        listLoading_ = false;
        body_.loading = false;
        draft_.loading = draft_.sending = false;
    }
}

void App::Pump() {
    // File dialogs run here, between frames: they're modal, and a frame
    // mustn't be left half-drawn while one is open.
    if (pickFiles_) {
        pickFiles_ = false;
        auto paths = PickFiles(hwnd_);
        if (!paths.empty()) attach(paths);
    }
    if (saveAttachment_ >= 0) {
        int index = saveAttachment_;
        saveAttachment_ = -1;
        for (const auto& a : body_.attachments)
            if (a.index == index) {
                std::wstring path = PickSavePath(hwnd_, Widen(a.name));
                if (!path.empty())
                    request({{"t", "save"}, {"id", body_.id}, {"index", index}, {"path", Narrow(path)}});
            }
    }
    std::vector<json> events;
    if (engine_) engine_->Drain(events);
    for (const auto& e : events) onEvent(e);

    for (UINT key : reader_->TakeKeys()) {
        if (key == VK_ESCAPE) {
            reader_->GiveFocusBack();
            select(0);
        }
        if (key == VK_F5) request({{"t", "sync"}});
    }
    if (listStale_ && !listLoading_) loadList(false);
    saveDraft(false);

    reader_->Tick(IsWindowVisible(hwnd_) && !IsIconic(hwnd_));
    if (GetForegroundWindow() == hwnd_) newCount_ = 0;
}

// ---------------------------------------------------------------- test steps

// testStep runs TRASHBOX_STEPS in demo mode: "wait 1.5", "select 2"
// (the third row), "folder 0 Archive", "unified", "remote", "waitload",
// "shot C:\\path.png", "mouse x y", "down", "up" (ImGui only), "new",
// "reply", "replyall", "set to|cc|subject|body text" (body: put before the
// quote), "attach C:\\file", "saveatt <index> C:\\path", "send", "discard",
// "waitdraft", "quit".
void App::testStep() {
    if (step_ >= steps_.size()) return;
    double now = ImGui::GetTime();
    if (stepWait_ > now) return;
    std::stringstream ss(steps_[step_]);
    std::string cmd;
    ss >> cmd;
    if (cmd == "wait") {
        double s = 1;
        ss >> s;
        stepWait_ = now + s;
    } else if (cmd == "waitload") {
        if (listLoading_ || body_.loading || reader_->Loading()) return;
        stepWait_ = now + 0.4;  // let the page paint
    } else if (cmd == "select") {
        size_t i = 0;
        ss >> i;
        if (i < list_.size()) select(list_[i].id);
    } else if (cmd == "folder") {
        size_t a = 0;
        std::string name;
        ss >> a;
        std::getline(ss >> std::ws, name);
        if (a < accounts_.size())
            for (const auto& f : accounts_[a].folders)
                if (f.name == name) {
                    view_ = View{false, accounts_[a].id, f.id};
                    list_.clear();
                    select(0);
                    loadList(false);
                }
    } else if (cmd == "unified") {
        view_ = View{};
        list_.clear();
        select(0);
        loadList(false);
    } else if (cmd == "mouse" || cmd == "down" || cmd == "up") {
        // Simulated mouse, fed to ImGui only: never the real cursor.
        ImGuiIO& io = ImGui::GetIO();
        if (cmd == "mouse") {
            float mx = 0, my = 0;
            ss >> mx >> my;
            io.AddMousePosEvent(mx, my);
        } else {
            io.AddMouseButtonEvent(0, cmd == "down");
        }
    } else if (cmd == "new") {
        newMessage();
    } else if (cmd == "reply" || cmd == "replyall") {
        reply(cmd == "replyall");
    } else if (cmd == "waitdraft") {
        if (draft_.loading || draft_.sending) return;
    } else if (cmd == "set") {
        // "set to a@b.com", "set body Hello\nthere" (\n for new lines)
        std::string field, text;
        ss >> field;
        std::getline(ss >> std::ws, text);
        for (size_t i; (i = text.find("\\n")) != std::string::npos;) text.replace(i, 2, "\n");
        std::string* f = field == "to" ? &draft_.to : field == "cc" ? &draft_.cc : field == "subject" ? &draft_.subject
                         : field == "body" ? &draft_.body : nullptr;
        if (f) {
            ImGui::ClearActiveID();  // as if typed: the field mustn't hold its own copy
            *f = field == "body" ? text + draft_.body : text;
            draft_.edited = true;
        }
    } else if (cmd == "send") {
        sendDraft();
    } else if (cmd == "attach") {
        std::string path;
        std::getline(ss >> std::ws, path);
        attach({Widen(path)});
    } else if (cmd == "saveatt") {
        int index = 0;
        std::string path;
        ss >> index;
        std::getline(ss >> std::ws, path);
        request({{"t", "save"}, {"id", body_.id}, {"index", index}, {"path", path}});
    } else if (cmd == "discard") {
        discardDraft();
    } else if (cmd == "remote") {
        if (body_.id) openBody(body_.id, true);
    } else if (cmd == "shot") {
        std::string path;
        std::getline(ss >> std::ws, path);
        SaveWindowPng(hwnd_, Widen(path));
    } else if (cmd == "quit") {
        // Hidden first without activating anything, so closing a test run
        // never moves the foreground to some other window.
        SetWindowPos(hwnd_, HWND_BOTTOM, 0, 0, 0, 0, SWP_NOMOVE | SWP_NOSIZE | SWP_NOACTIVATE | SWP_HIDEWINDOW);
        DestroyWindow(hwnd_);
    }
    step_++;
}

// ---------------------------------------------------------------- drawing

bool App::iconButton(const char* glyph, const char* tip, float size, bool danger) {
    const Palette& p = Colours();
    ImGui::PushStyleColor(ImGuiCol_Button, ImVec4(0, 0, 0, 0));
    ImGui::PushStyleColor(ImGuiCol_ButtonHovered, danger ? Hex(0xC42B1C) : p.hover);
    ImGui::PushStyleColor(ImGuiCol_ButtonActive, danger ? Hex(0xC42B1C) : p.border);
    ImGui::PushStyleColor(ImGuiCol_Text, p.muted);
    ImGui::PushFont(fonts_.regular, kSmall + 0.5f);
    bool pressed = ImGui::Button(glyph, ImVec2(size * scale_, size * scale_ * 0.87f));
    ImGui::PopFont();
    ImGui::PopStyleColor(4);
    hot_.push_back({ImGui::GetItemRectMin(), ImGui::GetItemRectMax()});
    if (tip && *tip && ImGui::IsItemHovered(ImGuiHoveredFlags_DelayNormal)) ImGui::SetTooltip("%s", tip);
    return pressed;
}

void App::Frame() {
    hot_.clear();
    testStep();
    const Palette& p = Colours();
    const float k = scale_;
    ImGuiViewport* vp = ImGui::GetMainViewport();
    ImGui::SetNextWindowPos(vp->Pos);
    ImGui::SetNextWindowSize(vp->Size);
    ImGui::Begin("##root", nullptr,
                 ImGuiWindowFlags_NoDecoration | ImGuiWindowFlags_NoMove | ImGuiWindowFlags_NoSavedSettings |
                     ImGuiWindowFlags_NoBringToFrontOnFocus | ImGuiWindowFlags_NoScrollWithMouse);
    float w = vp->Size.x, h = vp->Size.y;
    drawTitle(w);
    float top = 38 * k;
    float sideW = w >= 1000 * k ? 230 * k : 0;
    // The list's share of the room is the user's (drag the divider); both
    // sides keep a usable minimum.
    float room = w - sideW;
    float minList = 300 * k, minRead = 320 * k;
    float listW = room * cfg_.listShare;
    listW = std::max(minList, std::min(listW, room - minRead));
    if (room - listW < minRead) listW = std::max(200 * k, room - minRead);
    if (sideW > 0) drawSidebar(0, top, sideW, h - top);
    // A thin gap between the list and the pane is the divider: neither the
    // list's scrollbar nor the pane's browser window covers it.
    float gap = 5 * k;
    drawList(sideW, top, listW, h - top);
    if (draft_.shown) drawCompose(sideW + listW + gap, top, w - sideW - listW - gap, h - top);
    else drawReading(sideW + listW + gap, top, w - sideW - listW - gap, h - top);
    drawDivider(sideW, sideW + listW, top, h - top, room, sideW + minList, w - minRead);

    // Keys, unless something's taking typing.
    if (!ImGui::GetIO().WantTextInput) {
        auto move = [&](int d) {
            if (list_.empty()) return;
            int i = -1;
            for (size_t n = 0; n < list_.size(); n++)
                if (list_[n].id == selected_) i = int(n);
            int next = std::clamp(i < 0 ? 0 : i + d, 0, int(list_.size()) - 1);
            select(list_[size_t(next)].id);
            if (next >= int(list_.size()) - 5 && listMore_ && !listLoading_) loadList(true);
        };
        if (ImGui::IsKeyPressed(ImGuiKey_DownArrow) || ImGui::IsKeyPressed(ImGuiKey_J)) move(1);
        if (ImGui::IsKeyPressed(ImGuiKey_UpArrow) || ImGui::IsKeyPressed(ImGuiKey_K)) move(-1);
        if (ImGui::IsKeyPressed(ImGuiKey_Escape) && !draft_.shown) select(0);
        // Writing: N (or Ctrl+N) new, R reply, A or Ctrl+Shift+R reply all.
        ImGuiIO& io = ImGui::GetIO();
        if (ImGui::IsKeyPressed(ImGuiKey_N, false) && !io.KeyAlt) newMessage();
        if (ImGui::IsKeyPressed(ImGuiKey_R, false) && !io.KeyAlt && !draft_.shown) reply(io.KeyShift && io.KeyCtrl);
        if (ImGui::IsKeyPressed(ImGuiKey_A, false) && !io.KeyCtrl && !io.KeyAlt && !draft_.shown) reply(true);
        if (ImGui::IsKeyPressed(ImGuiKey_F5)) request({{"t", "sync"}});
        if (ImGui::IsKeyPressed(ImGuiKey_U) && body_.id && !draft_.shown) {
            for (auto& m : list_)
                if (m.id == body_.id) {
                    markSeen(m.id, !m.seen);
                }
        }
    }
    ImGui::End();

    bool popup = ImGui::IsPopupOpen("", ImGuiPopupFlags_AnyPopupId);
    paneHidden_ = popup || draft_.shown || body_.loading || !body_.error.empty() || body_.id == 0;
    reader_->Place(paneRect_, paneHidden_);
    (void)p;
}

// drawDivider is the grab strip on the list's side of the border with the
// reading pane (the pane itself is a separate window that takes the mouse).
// Dragging sets the list's share of the room; it's saved on letting go.
void App::drawDivider(float left, float x, float y, float h, float room, float minX, float maxX) {
    const Palette& p = Colours();
    const float k = scale_;
    float gap = 5 * k;
    ImGui::SetCursorScreenPos(ImVec2(x, y));
    ImGui::InvisibleButton("##divider", ImVec2(gap, h));
    bool hov = ImGui::IsItemHovered(), active = ImGui::IsItemActive();
    ImDrawList* dl = ImGui::GetWindowDrawList();
    dl->AddRectFilled(ImVec2(x, y), ImVec2(x + gap, y + h), U32(p.rail));
    if (hov || active) {
        ImGui::SetMouseCursor(ImGuiMouseCursor_ResizeEW);
        dl->AddRectFilled(ImVec2(x + 1 * k, y), ImVec2(x + gap - 1 * k, y + h), U32(p.accent, active ? 0.9f : 0.5f));
    }
    if (active && ImGui::IsMouseDragging(ImGuiMouseButton_Left, 0)) {
        float nx = std::clamp(ImGui::GetIO().MousePos.x, minX, maxX);
        cfg_.listShare = std::clamp((nx - left) / room, 0.1f, 0.9f);
    }
    if (ImGui::IsItemDeactivated()) saveConfig();
    if (hov && ImGui::IsMouseDoubleClicked(ImGuiMouseButton_Left)) {
        cfg_.listShare = 0.56f;  // back to the default
        saveConfig();
    }
    if (!active && ImGui::IsItemHovered(ImGuiHoveredFlags_DelayNormal)) ImGui::SetTooltip("Drag to resize; double-click to reset");
}

void App::drawTitle(float width) {
    const Palette& p = Colours();
    const float k = scale_;
    const float h = 38 * k;
    barMax_ = ImVec2(width, h);
    ImDrawList* dl = ImGui::GetWindowDrawList();
    dl->AddRectFilled(ImVec2(0, 0), ImVec2(width, h), U32(p.rail));
    ImGui::SetCursorPos(ImVec2(14 * k, 9 * k));
    ImGui::PushFont(fonts_.bold, 16);
    ImGui::TextColored(p.accent, "trashbox");
    ImGui::PopFont();

    // Sync state: quiet when all's well.
    int syncing = 0, failed = 0;
    for (const auto& a : accounts_) {
        if (a.status == "syncing" || a.status == "connecting") syncing++;
        if (a.status == "error") failed++;
    }
    ImGui::SameLine(0, 14 * k);
    ImGui::SetCursorPosY(12 * k);
    ImGui::PushFont(fonts_.regular, kSmall + 0.5f);
    if (!engineError_.empty()) ImGui::TextColored(p.bad, "%s", engineError_.c_str());
    else if (failed) {
        ImGui::TextColored(p.idle, "%s %d account%s can't connect", icon::Warning, failed, failed == 1 ? "" : "s");
        hot_.push_back({ImGui::GetItemRectMin(), ImGui::GetItemRectMax()});
        if (ImGui::IsItemHovered()) {
            std::string tip;
            for (const auto& a : accounts_)
                if (a.status == "error") tip += a.id + ": " + a.error + "\n";
            ImGui::SetTooltip("%s", tip.c_str());
        }
    } else if (syncing) ImGui::TextColored(p.muted, "Checking %d account%s\xE2\x80\xA6", syncing, syncing == 1 ? "" : "s");
    ImGui::PopFont();

    // A sign-in code from new mail, centred, with a Copy button.
    if (!code_.code.empty() && ImGui::GetTime() < code_.until) {
        ImGui::PushFont(fonts_.regular, kBody);
        std::string label = "Code from " + code_.from + ":";
        float lw = ImGui::CalcTextSize(label.c_str()).x;
        ImGui::PushFont(fonts_.bold, kBody + 2);
        float cw = ImGui::CalcTextSize(code_.code.c_str()).x;
        ImGui::PopFont();
        float bw = 64 * k, total = lw + 8 * k + cw + 10 * k + bw + 26 * k;
        float cx = std::max(170 * k, (width - total) / 2);
        ImDrawList* tdl = ImGui::GetWindowDrawList();
        tdl->AddRectFilled(ImVec2(cx - 10 * k, 5 * k), ImVec2(cx + total + 4 * k, h - 5 * k), U32(p.input), 4 * k);
        float ty = (h - ImGui::GetFontSize()) / 2;
        tdl->AddText(ImVec2(cx, ty), U32(p.muted), label.c_str());
        ImGui::PushFont(fonts_.bold, kBody + 2);
        tdl->AddText(ImVec2(cx + lw + 8 * k, (h - ImGui::GetFontSize()) / 2), U32(p.heading), code_.code.c_str());
        ImGui::PopFont();
        ImGui::SetCursorPos(ImVec2(cx + lw + 8 * k + cw + 10 * k, 8 * k));
        ImGui::PushStyleVar(ImGuiStyleVar_FramePadding, ImVec2(6 * k, 2 * k));
        bool copied = ImGui::GetTime() - code_.copiedAt < 2;
        if (ImGui::Button(copied ? "Copied" : "Copy", ImVec2(bw, h - 16 * k))) {
            CopySecret(hwnd_, code_.code);
            code_.copiedAt = ImGui::GetTime();
        }
        hot_.push_back({ImGui::GetItemRectMin(), ImGui::GetItemRectMax()});
        ImGui::SameLine(0, 4 * k);
        ImGui::PushStyleColor(ImGuiCol_Button, ImVec4(0, 0, 0, 0));
        if (ImGui::Button("\xC3\x97", ImVec2(20 * k, h - 16 * k))) code_ = {};
        ImGui::PopStyleColor();
        hot_.push_back({ImGui::GetItemRectMin(), ImGui::GetItemRectMax()});
        ImGui::PopStyleVar();
        ImGui::PopFont();
    }

    // Full-height buttons, glyphs centred, like Windows' own.
    float bw = 46 * k;
    float bx = width - 4 * bw;
    bool hiddenDraft = draft_.open && !draft_.shown;
    if (titleButton(icon::Draft, hiddenDraft ? "Back to your draft" : "New message (N)", bx - bw, bw, h)) {
        if (hiddenDraft) draft_.shown = true;
        else newMessage();
    }
    if (hiddenDraft)  // a dot: there's a draft waiting
        ImGui::GetWindowDrawList()->AddCircleFilled(ImVec2(bx - bw / 2 + 9 * k, h / 2 - 7 * k), 3.5f * k, U32(p.accent));
    if (titleButton(icon::Refresh, "Check for new mail (F5)", bx, bw, h)) request({{"t", "sync"}});
    if (titleButton(icon::Minimise, "", bx + bw, bw, h)) ::ShowWindow(hwnd_, SW_MINIMIZE);
    bool zoomed = IsZoomed(hwnd_);
    if (titleButton(zoomed ? icon::Restore : icon::Maximise, "", bx + 2 * bw, bw, h))
        ::ShowWindow(hwnd_, zoomed ? SW_RESTORE : SW_MAXIMIZE);
    if (titleButton(icon::Close, "", bx + 3 * bw, bw, h, true)) PostMessageW(hwnd_, WM_CLOSE, 0, 0);
}

bool App::titleButton(const char* glyph, const char* tip, float x, float w, float h, bool danger) {
    const Palette& p = Colours();
    ImGui::SetCursorPos(ImVec2(x, 0));
    ImGui::PushID(glyph);
    bool pressed = ImGui::InvisibleButton("##tb", ImVec2(w, h));
    bool hov = ImGui::IsItemHovered(), held = ImGui::IsItemActive();
    ImGui::PopID();
    ImVec2 a = ImGui::GetItemRectMin(), b = ImGui::GetItemRectMax();
    hot_.push_back({a, b});
    ImDrawList* dl = ImGui::GetWindowDrawList();
    if (hov || held) dl->AddRectFilled(a, b, danger ? U32(Hex(0xC42B1C)) : U32(held ? p.border : p.hover));
    ImGui::PushFont(fonts_.regular, 10);
    ImVec2 ts = ImGui::CalcTextSize(glyph);
    // Centre on the glyph's box, not the font's line: icon glyphs sit low.
    dl->AddText(ImVec2(a.x + (w - ts.x) / 2, a.y + (h - ts.y) / 2 - 1 * scale_), U32(hov && danger ? p.heading : p.muted),
                glyph);
    ImGui::PopFont();
    if (tip && *tip && hov && ImGui::IsItemHovered(ImGuiHoveredFlags_DelayNormal)) ImGui::SetTooltip("%s", tip);
    return pressed;
}

bool App::folderRow(const char* label, int unread, bool selected, float indent, bool strongCount) {
    const Palette& p = Colours();
    const float k = scale_;
    ImVec2 pos = ImGui::GetCursorScreenPos();
    float w = ImGui::GetContentRegionAvail().x;
    float h = 26 * k;
    ImGui::PushID(label);
    bool clicked = ImGui::InvisibleButton("##row", ImVec2(w, h));
    bool hovered = ImGui::IsItemHovered();
    ImGui::PopID();
    ImDrawList* dl = ImGui::GetWindowDrawList();
    if (selected) dl->AddRectFilled(pos, ImVec2(pos.x + w, pos.y + h), U32(p.selected));
    else if (hovered) dl->AddRectFilled(pos, ImVec2(pos.x + w, pos.y + h), U32(p.hover));
    if (selected) dl->AddRectFilled(pos, ImVec2(pos.x + 3 * k, pos.y + h), U32(p.accent));
    float ty = pos.y + (h - ImGui::GetFontSize()) / 2;
    char count[16] = "";
    if (unread > 0) snprintf(count, sizeof count, "%d", unread);
    float cw = count[0] ? ImGui::CalcTextSize(count).x : 0;
    ellipsis(dl, ImVec2(pos.x + 14 * k + indent, ty), pos.x + w - cw - 18 * k, label,
             U32(selected || (unread > 0 && strongCount) ? p.heading : p.text));
    if (count[0])
        dl->AddText(ImVec2(pos.x + w - cw - 12 * k, ty), U32(strongCount ? p.accent : p.muted), count);
    return clicked;
}

void App::drawSidebar(float x, float y, float w, float h) {
    const Palette& p = Colours();
    const float k = scale_;
    ImDrawList* bg = ImGui::GetWindowDrawList();
    bg->AddRectFilled(ImVec2(x, y), ImVec2(x + w, y + h), U32(p.sidebar));
    ImGui::SetCursorPos(ImVec2(x, y));
    ImGui::BeginChild("##sidebar", ImVec2(w, h), 0, ImGuiWindowFlags_NoScrollbar);
    ImGui::Dummy(ImVec2(0, 6 * k));

    auto changeView = [&](View v) {
        if (v == view_) return;
        view_ = v;
        list_.clear();
        listMore_ = false;
        scrollTop_ = true;
        select(0);
        loadList(false);
    };

    ImGui::PushFont(fonts_.semibold, kBody);
    std::string all = std::string(icon::Mail) + "  All inboxes";
    if (folderRow(all.c_str(), Unread(), view_.unified, 0, true)) changeView(View{});
    ImGui::PopFont();
    ImGui::Dummy(ImVec2(0, 4 * k));

    for (size_t ai = 0; ai < accounts_.size(); ai++) {
        Account& a = accounts_[ai];
        ImGui::PushID(a.id.c_str());
        // Account header: its colour, the address, and its state. Right-click
        // to move it or pick which folders show.
        ImVec2 pos = ImGui::GetCursorScreenPos();
        float rowH = 24 * k;
        if (ImGui::InvisibleButton("##acct", ImVec2(w, rowH))) a.collapsed = !a.collapsed;
        bool hov = ImGui::IsItemHovered();
        if (ImGui::BeginPopupContextItem("##acctmenu")) {
            if (ImGui::MenuItem("Move up", nullptr, false, ai > 0)) moveAccount(ai, -1);
            if (ImGui::MenuItem("Move down", nullptr, false, ai + 1 < accounts_.size())) moveAccount(ai, 1);
            ImGui::Separator();
            ImGui::TextDisabled("Folders to show");
            ImGui::PushItemFlag(ImGuiItemFlags_AutoClosePopups, false);
            std::function<void(const std::string&, int)> items = [&](const std::string& parent, int depth) {
                for (const auto& f : a.folders) {
                    if (f.parent != parent) continue;
                    bool on = folderShown(a, f);
                    std::string label = std::string(size_t(depth) * 4, ' ') + f.name + "##" + f.id;
                    if (ImGui::MenuItem(label.c_str(), nullptr, on)) setFolderShown(a, f, !on);
                    if (depth < 6) items(f.id, depth + 1);
                }
            };
            items("", 0);
            ImGui::PopItemFlag();
            ImGui::EndPopup();
        }
        ImDrawList* dl = ImGui::GetWindowDrawList();
        ImGui::PushFont(fonts_.regular, kSmall);
        float ty = pos.y + (rowH - ImGui::GetFontSize()) / 2;
        dl->AddText(ImVec2(pos.x + 10 * k, ty), U32(p.muted), a.collapsed ? icon::Right : icon::Down);
        dl->AddCircleFilled(ImVec2(pos.x + 30 * k, pos.y + rowH / 2), 3.5f * k, U32(accountColour(int(ai))));
        ImU32 stateCol = a.status == "error" ? U32(p.bad) : U32(hov ? p.text : p.muted);
        ellipsis(dl, ImVec2(pos.x + 40 * k, ty), pos.x + w - 10 * k, a.id, stateCol);
        ImGui::PopFont();
        if (hov && a.status == "error") ImGui::SetTooltip("%s", a.error.c_str());

        if (!a.collapsed) {
            // Only the folders ticked for this account: inbox and the special
            // folders first, then the rest in the server's order. A shown
            // folder whose parent is hidden sits at the top level.
            auto shownParent = [&](const Folder& f) {
                std::string parent = f.parent;
                for (int guard = 0; !parent.empty() && guard < 10; guard++) {
                    const Folder* pf = nullptr;
                    for (const auto& x2 : a.folders)
                        if (x2.id == parent) pf = &x2;
                    if (!pf) return std::string();
                    if (folderShown(a, *pf)) return parent;
                    parent = pf->parent;
                }
                return std::string();
            };
            std::vector<const Folder*> special, others;
            for (const auto& f : a.folders) {
                if (!folderShown(a, f)) continue;
                (f.role.empty() || !shownParent(f).empty() ? others : special).push_back(&f);
            }
            std::stable_sort(special.begin(), special.end(),
                             [](const Folder* l, const Folder* r) { return roleRank(l->role) < roleRank(r->role); });
            auto drawFolder = [&](const Folder& f, int depth) {
                View v{false, a.id, f.id};
                bool strong = f.role != "trash" && f.role != "junk" && f.role != "sent" && f.role != "drafts" &&
                              f.role != "all" && f.role != "archive";
                std::string label = std::string(roleIcon(f.role)) + "  " + f.name;
                ImGui::PushID(f.id.c_str());
                if (folderRow(label.c_str(), f.unread, !view_.unified && view_ == v, (14 + 14 * depth) * k, strong))
                    changeView(v);
                if (ImGui::BeginPopupContextItem("##foldermenu")) {
                    if (ImGui::MenuItem("Hide this folder")) setFolderShown(a, f, false);
                    ImGui::EndPopup();
                }
                ImGui::PopID();
            };
            std::function<void(const std::string&, int)> tree = [&](const std::string& parent, int depth) {
                for (const Folder* f : others)
                    if (shownParent(*f) == parent) {
                        drawFolder(*f, depth);
                        if (depth < 6) tree(f->id, depth + 1);
                    }
            };
            for (const Folder* f : special) {
                drawFolder(*f, 0);
                tree(f->id, 1);
            }
            tree("", 0);
        }
        ImGui::Dummy(ImVec2(0, 4 * k));
        ImGui::PopID();
    }
    if (accounts_.empty() && engineError_.empty()) {
        ImGui::SetCursorPosX(14 * k);
        ImGui::TextColored(p.muted, "Starting\xE2\x80\xA6");
    }
    ImGui::EndChild();
}

void App::drawList(float x, float y, float w, float h) {
    const Palette& p = Colours();
    const float k = scale_;
    ImDrawList* bg = ImGui::GetWindowDrawList();
    bg->AddRectFilled(ImVec2(x, y), ImVec2(x + w, y + h), U32(p.bg));

    // Heading: what's shown.
    std::string title = "All inboxes";
    if (!view_.unified)
        for (const auto& a : accounts_)
            if (a.id == view_.account)
                for (const auto& f : a.folders)
                    if (f.id == view_.folder) title = f.name;
    // One line: what's shown, and whose it is; then the column names.
    ImGui::SetCursorPos(ImVec2(x + 14 * k, y + 8 * k));
    ImGui::PushFont(fonts_.semibold, kBody + 1);
    ImGui::TextUnformatted(title.c_str());
    ImGui::PopFont();
    if (!view_.unified) {
        ImGui::SameLine(0, 8 * k);
        ImGui::PushFont(fonts_.regular, kSmall + 0.5f);
        ImGui::SetCursorPosY(y + 10 * k);
        ImVec2 at = ImGui::GetCursorScreenPos();
        ellipsis(ImGui::GetWindowDrawList(), at, x + w - 14 * k, view_.account, U32(p.muted));
        ImGui::PopFont();
    }
    float cols = 36 * k, head = cols + 24 * k;
    {
        float avail = w - 1 - ImGui::GetStyle().ScrollbarSize;
        float markW = (view_.unified ? 14 * k : 0) + 46 * k;
        float dateW = 118 * k;
        float fromW = std::clamp(avail * 0.28f, 120 * k, 260 * k);
        bg->AddRectFilled(ImVec2(x, y + cols), ImVec2(x + w - 1, y + head), U32(p.sidebar));
        bg->AddLine(ImVec2(x, y + head - 1), ImVec2(x + w - 1, y + head - 1), U32(p.border));
        ImGui::PushFont(fonts_.semibold, kSmall + 0.5f);
        float ty = y + cols + (24 * k - ImGui::GetFontSize()) / 2;
        bg->AddText(ImVec2(x + markW, ty), U32(p.muted), "Subject");
        bg->AddText(ImVec2(x + avail - dateW - fromW, ty), U32(p.muted), "From");
        bg->AddText(ImVec2(x + avail - dateW, ty), U32(p.muted), "Date");
        ImGui::PopFont();
    }
    ImGui::SetCursorPos(ImVec2(x, y + head));
    ImGui::BeginChild("##list", ImVec2(w - 1, h - head), 0);
    if (scrollTop_) {
        ImGui::SetScrollY(0);
        scrollTop_ = false;
    }

    // A table, one line a message: marks, subject, from, date.
    const float rowH = 27 * k;
    int64_t now = int64_t(std::time(nullptr));
    float avail = ImGui::GetContentRegionAvail().x;
    float markW = (view_.unified ? 14 * k : 0) + 46 * k;
    float dateW = 118 * k;
    float fromW = std::clamp(avail * 0.28f, 120 * k, 260 * k);
    float subjX = markW, fromX = avail - dateW - fromW, dateX = avail - dateW;
    ImGuiListClipper clip;
    clip.Begin(int(list_.size()), rowH);
    if (scrollToSelected_) {
        for (size_t i = 0; i < list_.size(); i++)
            if (list_[i].id == selected_) {
                clip.IncludeItemByIndex(int(i));
                float top = i * rowH, sy = ImGui::GetScrollY(), vh = ImGui::GetWindowHeight();
                if (top < sy) ImGui::SetScrollY(top);
                else if (top + rowH > sy + vh) ImGui::SetScrollY(top + rowH - vh);
            }
        scrollToSelected_ = false;
    }
    bool nearEnd = false;
    while (clip.Step()) {
        for (int i = clip.DisplayStart; i < clip.DisplayEnd; i++) {
            Msg& m = list_[size_t(i)];
            if (i >= int(list_.size()) - 20) nearEnd = true;
            ImVec2 pos = ImGui::GetCursorScreenPos();
            float rw = ImGui::GetContentRegionAvail().x;
            ImGui::PushID(int(m.id));
            if (ImGui::InvisibleButton("##m", ImVec2(rw, rowH))) {
                reader_->GiveFocusBack();
                select(m.id);
            }
            bool hov = ImGui::IsItemHovered();
            if (ImGui::BeginPopupContextItem("##ctx")) {
                if (ImGui::MenuItem(m.seen ? "Mark as unread" : "Mark as read")) markSeen(m.id, !m.seen);
                ImGui::EndPopup();
            }
            ImGui::PopID();
            ImDrawList* dl = ImGui::GetWindowDrawList();
            bool sel = m.id == selected_;
            if (sel) dl->AddRectFilled(pos, ImVec2(pos.x + rw, pos.y + rowH), U32(p.selected));
            else if (hov) dl->AddRectFilled(pos, ImVec2(pos.x + rw, pos.y + rowH), U32(p.hover));
            else if (i % 2) dl->AddRectFilled(pos, ImVec2(pos.x + rw, pos.y + rowH), U32(p.sidebar, 0.45f));

            ImFont* face = m.seen ? fonts_.regular : fonts_.bold;
            ImU32 strong = U32(m.seen ? p.text : p.heading);
            ImGui::PushFont(fonts_.regular, kBody);
            float ty = pos.y + (rowH - ImGui::GetFontSize()) / 2;
            float mx = pos.x + 8 * k;
            if (view_.unified) {
                dl->AddCircleFilled(ImVec2(mx + 4 * k, pos.y + rowH / 2), 3.5f * k, U32(accountColour(accountIndex(m.account))));
                mx += 14 * k;
            }
            ImGui::PushFont(fonts_.regular, kSmall);
            float iy = pos.y + (rowH - ImGui::GetFontSize()) / 2 + 1 * k;
            if (m.flagged) dl->AddText(ImVec2(mx, iy), U32(p.bad), icon::Flag);
            if (m.attach) dl->AddText(ImVec2(mx + 17 * k, iy), U32(p.muted), icon::Attach);
            ImGui::PopFont();
            ImGui::PopFont();

            ImGui::PushFont(face, kBody);
            std::string subject = m.subject.empty() ? "(no subject)" : oneLine(m.subject);
            ellipsis(dl, ImVec2(pos.x + subjX, ty), pos.x + fromX - 12 * k, subject, strong);
            std::string from = m.fromName.empty() ? m.fromAddr : m.fromName;
            ellipsis(dl, ImVec2(pos.x + fromX, ty), pos.x + dateX - 12 * k, oneLine(from), U32(m.seen ? p.muted : p.text));
            ImGui::PopFont();
            ImGui::PushFont(fonts_.regular, kBody - 0.5f);
            std::string date = TableDate(m.date, now);
            dl->AddText(ImVec2(pos.x + dateX, pos.y + (rowH - ImGui::GetFontSize()) / 2), U32(m.seen ? p.muted : p.accent),
                        date.c_str());
            ImGui::PopFont();
        }
    }
    clip.End();
    if (nearEnd && listMore_ && !listLoading_ && !list_.empty()) loadList(true);

    if (list_.empty()) {
        ImGui::SetCursorPos(ImVec2(16 * k, 16 * k));
        ImGui::PushFont(fonts_.regular, kSmall + 0.5f);
        ImGui::TextColored(p.muted, "%s", listLoading_ ? "Loading\xE2\x80\xA6" : "Nothing here.");
        ImGui::PopFont();
    } else if (listLoading_ && listMore_) {
        ImGui::SetCursorPosX(16 * k);
        ImGui::PushFont(fonts_.regular, kSmall);
        ImGui::TextColored(p.muted, "Loading older mail\xE2\x80\xA6");
        ImGui::PopFont();
        ImGui::Dummy(ImVec2(0, 8 * k));
    }
    ImGui::EndChild();

    // A note along the bottom (new mail, errors).
    if (!note_.empty() && ImGui::GetTime() < noteUntil_) {
        ImGui::PushFont(fonts_.regular, kSmall);
        ImDrawList* dl = ImGui::GetWindowDrawList();
        float nh = 30 * k;
        ImVec2 a(x + 8 * k, y + h - nh - 8 * k), b(x + w - 9 * k, y + h - 8 * k);
        dl->AddRectFilled(a, b, U32(p.input), 4 * k);
        ellipsis(dl, ImVec2(a.x + 10 * k, a.y + (nh - ImGui::GetFontSize()) / 2), b.x - 10 * k, oneLine(note_), U32(p.text));
        ImGui::PopFont();
    }
}

void App::drawReading(float x, float y, float w, float h) {
    const Palette& p = Colours();
    const float k = scale_;
    ImDrawList* dl = ImGui::GetWindowDrawList();
    dl->AddRectFilled(ImVec2(x, y), ImVec2(x + w, y + h), U32(p.bg));
    paneRect_ = RECT{LONG(x), LONG(y + h), LONG(x + w), LONG(y + h)};

    if (body_.id == 0) {
        ImGui::PushFont(fonts_.regular, kBody);
        const char* t = list_.empty() ? "" : "Pick a message to read it.";
        ImVec2 ts = ImGui::CalcTextSize(t);
        dl->AddText(ImVec2(x + (w - ts.x) / 2, y + h * 0.42f), U32(p.muted), t);
        ImGui::PopFont();
        return;
    }
    const Msg* m = nullptr;
    for (const auto& x2 : list_)
        if (x2.id == body_.id) m = &x2;

    float pad = 22 * k;
    // Reply and Reply all, top right.
    float btnsW = 0;
    if (!body_.loading && body_.error.empty()) {
        ImGui::PushFont(fonts_.regular, kSmall + 1);
        ImGui::PushStyleVar(ImGuiStyleVar_FramePadding, ImVec2(10 * k, 5 * k));
        std::string r1 = std::string(icon::Reply) + "  Reply", r2 = std::string(icon::ReplyAll) + "  Reply all";
        float w1 = ImGui::CalcTextSize(r1.c_str()).x + 20 * k, w2 = ImGui::CalcTextSize(r2.c_str()).x + 20 * k;
        btnsW = w1 + w2 + 6 * k + 12 * k;
        ImGui::SetCursorPos(ImVec2(x + w - pad - w1 - w2 - 6 * k, y + 14 * k));
        if (ImGui::Button(r1.c_str(), ImVec2(w1, 0))) reply(false);
        if (ImGui::IsItemHovered(ImGuiHoveredFlags_DelayNormal)) ImGui::SetTooltip("Reply to the sender (R)");
        ImGui::SameLine(0, 6 * k);
        if (ImGui::Button(r2.c_str(), ImVec2(w2, 0))) reply(true);
        if (ImGui::IsItemHovered(ImGuiHoveredFlags_DelayNormal)) ImGui::SetTooltip("Reply to everyone on it (A)");
        ImGui::PopStyleVar();
        ImGui::PopFont();
    }
    ImGui::SetCursorPos(ImVec2(x + pad, y + 16 * k));
    ImGui::BeginGroup();
    float textW = w - 2 * pad;
    ImGui::PushTextWrapPos(x + pad + textW - btnsW);
    // Subject.
    std::string subject = !body_.subject.empty() ? body_.subject : m ? m->subject : "";
    ImGui::PushFont(fonts_.semibold, kH1 + 2);
    ImGui::TextUnformatted(subject.empty() ? "(no subject)" : subject.c_str());
    ImGui::PopFont();
    ImGui::Dummy(ImVec2(0, 4 * k));
    // From, with the address in full: it's what tells a real sender from a fake.
    if (!body_.from.empty()) {
        const Addr& f = body_.from[0];
        ImGui::PushFont(fonts_.semibold, kH3 + 0.5f);
        ImGui::TextUnformatted(f.name.empty() ? f.addr.c_str() : f.name.c_str());
        ImGui::PopFont();
        if (!f.name.empty()) {
            ImGui::SameLine(0, 6 * k);
            ImGui::PushFont(fonts_.regular, kBody + 0.5f);
            ImGui::SetCursorPosY(ImGui::GetCursorPosY() + 1 * k);
            ImGui::TextColored(p.muted, "<%s>", f.addr.c_str());
            ImGui::PopFont();
        }
    } else if (m) {
        ImGui::PushFont(fonts_.semibold, kBody);
        ImGui::TextUnformatted(m->fromName.empty() ? m->fromAddr.c_str() : m->fromName.c_str());
        ImGui::PopFont();
    }
    ImGui::PopTextWrapPos();
    ImGui::PushFont(fonts_.regular, kBody + 0.5f);
    float lineMax = x + w - pad;
    auto line = [&](const std::string& text) {
        ImVec2 at = ImGui::GetCursorScreenPos();
        ellipsis(ImGui::GetWindowDrawList(), at, lineMax, text, U32(p.muted));
        ImGui::Dummy(ImVec2(textW, ImGui::GetFontSize() + 3 * k));
    };
    if (!body_.to.empty()) line("To: " + addrList(body_.to));
    if (!body_.cc.empty()) line("Cc: " + addrList(body_.cc));
    int64_t date = body_.date ? body_.date : m ? m->date : 0;
    std::string where = m ? m->account : "";
    if (date) line(LongDate(date) + (where.empty() ? "" : "  \xC2\xB7  " + where));
    ImGui::PopFont();

    // Attachments.
    if (!body_.attachments.empty()) {
        ImGui::Dummy(ImVec2(0, 4 * k));
        ImGui::PushFont(fonts_.regular, kSmall + 1);
        float cx = x + pad;
        for (const auto& a : body_.attachments) {
            std::string label = std::string(icon::Attach) + " " + a.name + "  " + Size(a.size);
            ImVec2 ts = ImGui::CalcTextSize(label.c_str());
            float cw = ts.x + 18 * k;
            if (cx + cw > x + w - pad && cx > x + pad) {
                ImGui::NewLine();
                cx = x + pad;
            }
            ImVec2 at(cx, ImGui::GetCursorScreenPos().y);
            ImVec2 save = ImGui::GetCursorScreenPos();
            ImGui::SetCursorScreenPos(at);
            ImGui::PushID(a.index);
            bool clicked = ImGui::InvisibleButton("##att", ImVec2(cw, ts.y + 10 * k));
            bool hov = ImGui::IsItemHovered();
            ImGui::PopID();
            ImGui::SetCursorScreenPos(save);
            if (clicked) saveAttachment_ = a.index;
            if (hov) {
                ImGui::SetMouseCursor(ImGuiMouseCursor_Hand);
                ImGui::SetTooltip("Save %s\xE2\x80\xA6", a.name.c_str());
            }
            ImGui::GetWindowDrawList()->AddRectFilled(at, ImVec2(at.x + cw, at.y + ts.y + 10 * k), U32(hov ? p.hover : p.input),
                                                      4 * k);
            ImGui::GetWindowDrawList()->AddText(ImVec2(at.x + 9 * k, at.y + 5 * k), U32(p.text), label.c_str());
            cx += cw + 6 * k;
        }
        ImGui::Dummy(ImVec2(textW, ImGui::GetFontSize() + 12 * k));
        ImGui::PopFont();
    }

    // Pictures from the web stay hidden until asked for.
    if (body_.hasRemote && !body_.remote && !body_.loading) {
        ImGui::Dummy(ImVec2(0, 4 * k));
        ImVec2 at = ImGui::GetCursorScreenPos();
        float bh = 34 * k;
        ImGui::GetWindowDrawList()->AddRectFilled(at, ImVec2(x + w - pad, at.y + bh), U32(p.input), 4 * k);
        ImGui::PushFont(fonts_.regular, kSmall + 0.5f);
        ImGui::GetWindowDrawList()->AddText(ImVec2(at.x + 12 * k, at.y + (bh - ImGui::GetFontSize()) / 2), U32(p.muted),
                                            (std::string(icon::Picture) + "  Pictures from the web are hidden.").c_str());
        ImGui::SetCursorScreenPos(ImVec2(x + w - pad - 120 * k, at.y + 4 * k));
        ImGui::PushStyleVar(ImGuiStyleVar_FramePadding, ImVec2(10 * k, 4 * k));
        if (ImGui::Button("Show pictures", ImVec2(112 * k, bh - 8 * k))) openBody(body_.id, true);
        ImGui::PopStyleVar();
        ImGui::PopFont();
        ImGui::SetCursorScreenPos(ImVec2(at.x, at.y + bh));
        ImGui::Dummy(ImVec2(textW, 2 * k));
    }
    ImGui::EndGroup();

    float top = ImGui::GetCursorScreenPos().y + 10 * k;
    dl->AddLine(ImVec2(x, top - 1), ImVec2(x + w, top - 1), U32(p.border));
    if (!body_.error.empty()) {
        ImGui::SetCursorScreenPos(ImVec2(x + pad, top + 16 * k));
        ImGui::PushTextWrapPos(x + w - pad);
        ImGui::TextColored(p.bad, "Couldn't open this message: %s", body_.error.c_str());
        ImGui::PopTextWrapPos();
        if (ImGui::Button("Try again")) openBody(body_.id, body_.remote);
    } else if (body_.loading || reader_->Loading()) {
        ImGui::PushFont(fonts_.regular, kSmall + 0.5f);
        dl->AddText(ImVec2(x + pad, top + 16 * k), U32(p.muted), "Loading\xE2\x80\xA6");
        ImGui::PopFont();
    } else if (!reader_->Error().empty()) {
        ImGui::SetCursorScreenPos(ImVec2(x + pad, top + 16 * k));
        ImGui::TextColored(p.bad, "%s", reader_->Error().c_str());
    }
    paneRect_ = RECT{LONG(x), LONG(top), LONG(x + w), LONG(y + h)};
}

// drawCompose is the draft, in place of the reading pane: who it's from and
// to, the subject, and the text. Ctrl+Enter sends.
void App::drawCompose(float x, float y, float w, float h) {
    const Palette& p = Colours();
    const float k = scale_;
    ImDrawList* dl = ImGui::GetWindowDrawList();
    dl->AddRectFilled(ImVec2(x, y), ImVec2(x + w, y + h), U32(p.bg));
    paneRect_ = RECT{LONG(x), LONG(y + h), LONG(x + w), LONG(y + h)};
    float pad = 22 * k;

    // Esc would undo everything typed since the field was clicked (ImGui's
    // "revert"): let go of the field first, so Esc only leaves it.
    if (ImGui::IsKeyPressed(ImGuiKey_Escape, false) && ImGui::GetIO().WantTextInput) ImGui::ClearActiveID();
    bool ctrlEnter = ImGui::GetIO().KeyCtrl &&
                     (ImGui::IsKeyPressed(ImGuiKey_Enter, false) || ImGui::IsKeyPressed(ImGuiKey_KeypadEnter, false));

    ImGui::SetCursorPos(ImVec2(x + pad, y + 14 * k));
    ImGui::BeginGroup();
    ImGui::PushFont(fonts_.semibold, kH1 + 2);
    ImGui::TextUnformatted(draft_.kind.c_str());
    ImGui::PopFont();
    ImGui::EndGroup();

    // Send and Discard, top right.
    ImGui::PushFont(fonts_.regular, kSmall + 1);
    ImGui::PushStyleVar(ImGuiStyleVar_FramePadding, ImVec2(12 * k, 5 * k));
    std::string sendLabel = std::string(icon::Send) + "  " + (draft_.sending ? "Sending\xE2\x80\xA6" : "Send");
    std::string attachLabel = std::string(icon::Attach) + "  Attach";
    float sw = ImGui::CalcTextSize(sendLabel.c_str()).x + 24 * k, dw = ImGui::CalcTextSize("Discard").x + 24 * k;
    float aw = ImGui::CalcTextSize(attachLabel.c_str()).x + 24 * k;
    ImGui::SetCursorPos(ImVec2(x + w - pad - sw - dw - aw - 12 * k, y + 14 * k));
    ImGui::BeginDisabled(draft_.sending || draft_.loading);
    if (ImGui::Button(attachLabel.c_str(), ImVec2(aw, 0))) pickFiles_ = true;
    if (ImGui::IsItemHovered(ImGuiHoveredFlags_DelayNormal)) ImGui::SetTooltip("Attach files (or drop them on the window)");
    ImGui::SameLine(0, 6 * k);
    if (ImGui::Button("Discard", ImVec2(dw, 0))) ImGui::OpenPopup("##discard");
    ImGui::SameLine(0, 6 * k);
    ImGui::PushStyleColor(ImGuiCol_Button, p.accent);
    ImGui::PushStyleColor(ImGuiCol_ButtonHovered, ImVec4(p.accent.x * 1.1f, p.accent.y * 1.1f, p.accent.z * 1.1f, 1));
    ImGui::PushStyleColor(ImGuiCol_ButtonActive, ImVec4(p.accent.x * 0.9f, p.accent.y * 0.9f, p.accent.z * 0.9f, 1));
    ImGui::PushStyleColor(ImGuiCol_Text, Hex(0xFFFFFF));
    if (ImGui::Button(sendLabel.c_str(), ImVec2(sw, 0)) || ctrlEnter) sendDraft();
    ImGui::PopStyleColor(4);
    if (ImGui::IsItemHovered(ImGuiHoveredFlags_DelayNormal | ImGuiHoveredFlags_AllowWhenDisabled))
        ImGui::SetTooltip("Send (Ctrl+Enter)");
    ImGui::EndDisabled();
    if (ImGui::BeginPopup("##discard")) {
        ImGui::TextUnformatted("Throw this draft away?");
        ImGui::Dummy(ImVec2(0, 4 * k));
        if (ImGui::Button("Discard")) {
            discardDraft();
            ImGui::CloseCurrentPopup();
        }
        ImGui::SameLine();
        if (ImGui::Button("Keep writing")) ImGui::CloseCurrentPopup();
        ImGui::EndPopup();
    }
    ImGui::PopStyleVar();
    ImGui::PopFont();
    if (!draft_.open) return;  // just discarded

    float fy = y + 58 * k;
    if (draft_.loading) {
        ImGui::PushFont(fonts_.regular, kSmall + 0.5f);
        dl->AddText(ImVec2(x + pad, fy), U32(p.muted), "Writing the reply\xE2\x80\xA6");
        ImGui::PopFont();
        return;
    }

    bool changed = false;
    float labelW = 64 * k, fieldW = w - 2 * pad - labelW;
    ImGui::PushFont(fonts_.regular, kBody);
    ImGui::PushStyleVar(ImGuiStyleVar_FramePadding, ImVec2(8 * k, 6 * k));
    float rowH = ImGui::GetFontSize() + 12 * k + 6 * k;
    auto label = [&](const char* text) {
        ImGui::SetCursorScreenPos(ImVec2(x + pad, fy + 6 * k));
        ImGui::TextColored(p.muted, "%s", text);
        ImGui::SetCursorScreenPos(ImVec2(x + pad + labelW, fy));
    };
    auto field = [&](const char* name, const char* id, std::string& s, int focusId, float width) {
        label(name);
        if (focusId && draft_.focus == focusId) {
            // By ID: SetKeyboardFocusHere's tabbing lands on the wrong field.
            ImGui::ActivateItemByID(ImGui::GetID(id));
            draft_.focus = 0;
        }
        changed |= inputString(id, s, ImVec2(width, 0), false);
        fy += rowH;
    };

    // From: one of the accounts.
    label("From");
    ImGui::SetNextItemWidth(fieldW);
    if (ImGui::BeginCombo("##from", draft_.from.empty() ? "Pick an account" : draft_.from.c_str())) {
        for (size_t i = 0; i < accounts_.size(); i++) {
            const Account& a = accounts_[i];
            if (ImGui::Selectable(a.id.c_str(), a.id == draft_.from)) {
                changed |= draft_.from != a.id;
                draft_.from = a.id;
            }
        }
        ImGui::EndCombo();
    }
    fy += rowH;
    field("To", "##to", draft_.to, 1, fieldW);
    // Cc, with a toggle for Bcc beside it.
    float bccW = 52 * k;
    field("Cc", "##cc", draft_.cc, 0, draft_.showBcc ? fieldW : fieldW - bccW - 6 * k);
    if (!draft_.showBcc) {
        ImGui::SetCursorScreenPos(ImVec2(x + w - pad - bccW, fy - rowH));
        if (ImGui::Button("Bcc", ImVec2(bccW, 0))) draft_.showBcc = true;
    } else {
        field("Bcc", "##bcc", draft_.bcc, 0, fieldW);
    }
    field("Subject", "##subject", draft_.subject, 0, fieldW);

    // Attached files, as chips with a cross to take each off.
    if (!draft_.attachments.empty()) {
        ImGui::PushFont(fonts_.regular, kSmall + 1);
        float cx = x + pad + labelW, chipH = ImGui::GetFontSize() + 12 * k;
        int64_t total = 0;
        int remove = -1;
        label("Files");
        for (size_t i = 0; i < draft_.attachments.size(); i++) {
            std::wstring wp = Widen(draft_.attachments[i]);
            int64_t size = FileSize(wp);
            total += std::max<int64_t>(size, 0);
            std::string text = std::string(icon::Attach) + " " + Narrow(BaseName(wp)) + "  " +
                               (size < 0 ? std::string("missing") : Size(size));
            float tw = ImGui::CalcTextSize(text.c_str()).x, cw = tw + 40 * k;
            if (cx + cw > x + w - pad && cx > x + pad + labelW) {
                cx = x + pad + labelW;
                fy += chipH + 6 * k;
            }
            ImVec2 a(cx, fy), b(cx + cw, fy + chipH);
            dl->AddRectFilled(a, b, U32(p.input), 4 * k);
            dl->AddText(ImVec2(a.x + 9 * k, a.y + 6 * k), U32(size < 0 ? p.bad : p.text), text.c_str());
            ImGui::SetCursorScreenPos(ImVec2(b.x - 24 * k, a.y + 2 * k));
            ImGui::PushID(int(i));
            ImGui::PushStyleColor(ImGuiCol_Button, ImVec4(0, 0, 0, 0));
            ImGui::PushStyleVar(ImGuiStyleVar_FramePadding, ImVec2(0, 0));
            if (ImGui::Button("\xC3\x97", ImVec2(20 * k, chipH - 4 * k))) remove = int(i);
            ImGui::PopStyleVar();
            ImGui::PopStyleColor();
            if (ImGui::IsItemHovered()) ImGui::SetTooltip("Take it off");
            ImGui::PopID();
            cx += cw + 6 * k;
        }
        fy += chipH + 6 * k;
        if (total > (25 << 20)) {
            ImGui::SetCursorScreenPos(ImVec2(x + pad + labelW, fy));
            ImGui::TextColored(p.bad, "These come to %s: one message can carry 25 MB.", Size(total).c_str());
            fy += ImGui::GetFontSize() + 6 * k;
        }
        ImGui::PopFont();
        if (remove >= 0) {
            draft_.attachments.erase(draft_.attachments.begin() + remove);
            changed = true;
        }
    }

    // The text.
    fy += 6 * k;
    float bodyH = y + h - fy - (draft_.error.empty() ? pad : pad + 30 * k);
    ImGui::SetCursorScreenPos(ImVec2(x + pad, fy));
    if (draft_.focus == 2) {
        ImGui::ActivateItemByID(ImGui::GetID("##body"));
        draft_.focus = 0;
    }
    ImGui::PushStyleVar(ImGuiStyleVar_FramePadding, ImVec2(12 * k, 10 * k));
    ImGui::PushFont(fonts_.regular, kBody + 0.5f);
    changed |= inputString("##body", draft_.body, ImVec2(w - 2 * pad, std::max(80 * k, bodyH)), true,
                           ImGuiInputTextFlags_WordWrap, &draft_.bodyToStart);
    ImGui::PopFont();
    ImGui::PopStyleVar(2);
    ImGui::PopFont();

    if (!draft_.error.empty()) {
        ImGui::SetCursorScreenPos(ImVec2(x + pad, y + h - pad - 22 * k));
        ImGui::PushTextWrapPos(x + w - pad);
        ImGui::TextColored(p.bad, "%s", draft_.error.c_str());
        ImGui::PopTextWrapPos();
    }
    if (changed) {
        draft_.edited = draft_.dirty = true;
        draft_.changedAt = ImGui::GetTime();
        draft_.error.clear();
    }
}

}  // namespace tb
