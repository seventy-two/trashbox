// Small helpers: UTF-8/UTF-16, dates and sizes for display, screenshots.
#pragma once

#include <windows.h>

#include <cstdint>
#include <string>
#include <string_view>

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

// SaveWindowPng captures a window (child windows included) to a PNG, for
// test screenshots.
bool SaveWindowPng(HWND hwnd, const std::wstring& path);

}  // namespace tb
