// Tests for the window's pure helpers. Run by build.bat.
#include <cstdio>
#include <ctime>
#include <string>

#include "util.h"

static int failures = 0;

static void expect(const std::string& got, const std::string& want, const char* what) {
    if (got != want) {
        std::printf("FAIL %s: got \"%s\", want \"%s\"\n", what, got.c_str(), want.c_str());
        failures++;
    }
}

static int64_t at(int y, int mo, int d, int h, int mi) {
    std::tm tm{};
    tm.tm_year = y - 1900;
    tm.tm_mon = mo - 1;
    tm.tm_mday = d;
    tm.tm_hour = h;
    tm.tm_min = mi;
    tm.tm_isdst = -1;
    return int64_t(std::mktime(&tm));
}

int main() {
    int64_t now = at(2026, 11, 24, 15, 0);  // a Tuesday
    expect(tb::ListDate(at(2026, 11, 24, 9, 5), now), "09:05", "today");
    expect(tb::ListDate(at(2026, 11, 23, 22, 0), now), "Mon", "yesterday");
    expect(tb::ListDate(at(2026, 11, 19, 12, 0), now), "Thu", "five days ago");
    expect(tb::ListDate(at(2026, 11, 2, 12, 0), now), "2 Nov", "this year");
    expect(tb::ListDate(at(2025, 12, 25, 12, 0), now), "25 Dec 2025", "last year");
    expect(tb::LongDate(at(2026, 11, 24, 14, 5)), "Tuesday 24 November 2026, 14:05", "long date");
    expect(tb::Size(820), "820 bytes", "bytes");
    expect(tb::Size(14 * 1024), "14 KB", "kilobytes");
    expect(tb::Size(2400000), "2.3 MB", "megabytes");
    expect(tb::Narrow(tb::Widen("Caf\xC3\xA9 \xF0\x9F\x8C\xB1")), "Caf\xC3\xA9 \xF0\x9F\x8C\xB1", "utf-8 round trip");
    if (failures) return 1;
    std::printf("tests passed\n");
    return 0;
}
