// Colours, fonts and ImGui style, light and dark after Windows' app theme.
#pragma once

#include <string_view>

#include "imgui.h"

namespace tb {

struct Palette {
    ImVec4 rail, sidebar, bg, text, muted, heading, accent, link, input, code, border, hover, selected;
    ImVec4 good, idle, dnd, bad, mentionBar, spoiler;
    ImVec4 band;  // every other author's messages sit on this
};

struct Fonts {
    ImFont* regular = nullptr;  // with the icons merged in
    ImFont* bold = nullptr;
    ImFont* italic = nullptr;
    ImFont* boldItalic = nullptr;
    ImFont* semibold = nullptr;  // headings, names; also with icons
    ImFont* mono = nullptr;
};

// Base sizes, before DPI scaling.
constexpr float kBody = 14.5f, kSmall = 12.5f, kH1 = 20, kH2 = 18, kH3 = 16, kJumbo = 40;

bool SystemUsesDarkTheme();
void ApplyTheme(bool dark, float scale);
const Palette& Colours();
// LoadFonts adds the fonts to ImGui's atlas, once at startup; with 1.92's
// dynamic atlas they're drawn at any size.
Fonts LoadFonts();

// NoteText looks for scripts the loaded fonts lack (Chinese, Japanese,
// Korean) in text about to be drawn. Their fonts are big, so they're only
// loaded once something needs them.
void NoteText(std::string_view text);
// LoadNeededFonts merges in the fonts NoteText found wanting. Call it
// between frames; it returns true if it added any, so layouts measured
// without them should be redone.
bool LoadNeededFonts(const Fonts& fonts);

ImU32 U32(const ImVec4& c, float alpha = 1.0f);
ImVec4 Hex(unsigned rgb, float a = 1.0f);

}  // namespace tb
