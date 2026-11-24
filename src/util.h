// Small helpers: UTF-8/UTF-16, dates and sizes for display, screenshots.
#pragma once

#include <windows.h>

#include <cstdint>
#include <string>
#include <string_view>
#include <vector>

namespace tb {

std::wstring Widen(std::string_view s);
std::string Narrow(std::wstring_view s);

// ListDate is a message's date in the list: "14:05" today, "Mon" this
// week, "24 Nov" this year, else "24 Nov 2025".
std::string ListDate(int64_t unix, int64_t now);
// TableDate is a message's date in the table: "14:05" today, "23 Nov 14:05"
// this year, else "23 Nov 2025".
std::string TableDate(int64_t unix, int64_t now);
// LongDate is the reading pane's: "Tuesday 24 November 2026, 14:05".
std::string LongDate(int64_t unix);
// Size is a file size: "820 bytes", "14 KB", "2.3 MB".
std::string Size(int64_t bytes);

// OpenExternal opens a web or mailto link in the default app. Anything
// else is refused.
bool OpenExternal(const std::wstring& url);

// CopySecret puts text on the clipboard, marked so Windows keeps it out of
// clipboard history and cloud clipboard (for sign-in codes).
void CopySecret(HWND owner, const std::string& text);

// PickFiles asks for files to attach (several at once); empty if cancelled.
std::vector<std::wstring> PickFiles(HWND owner);
// PickSavePath asks where to save a file called name, starting in
// Downloads; empty if cancelled.
std::wstring PickSavePath(HWND owner, const std::wstring& name);
// FileSize is a file's size, or -1 if it can't be read.
int64_t FileSize(const std::wstring& path);
// BaseName is the last part of a path.
std::wstring BaseName(const std::wstring& path);

// SaveWindowPng captures a window (child windows included) to a PNG, for
// test screenshots.
bool SaveWindowPng(HWND hwnd, const std::wstring& path);

}  // namespace tb
