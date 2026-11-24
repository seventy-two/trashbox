// Package codes finds one-time sign-in codes (2FA, verification, OTP) in
// emails, with plain rules: no AI, nothing leaves the machine to decide.
//
// It's deliberately cautious. An email must talk about a code in so many
// words ("verification code", "one-time", "2FA"...), and the code must sit
// close to that wording. Promo codes, order numbers, prices, dates, times
// and phone numbers are refused. Links are never codes: sign-in links are
// left alone.
package codes

import (
	"regexp"
	"strings"
	"unicode"
)

// strong wording: an email using none of these isn't about a code.
var strong = regexp.MustCompile(`(?i)\b(?:verification|security|sign[- ]?in|log[- ]?in|login|one[- ]time|single[- ]use|access|confirmation|authentication|auth|2fa|mfa|otp|passcode|pass code|two[- ]factor|2[- ]step|guard)\s*(?:code|pin|number|password)?\b|\bOTP\b|\bpasscode\b|\b(?:your|the|this|a|enter|use)\s+(?:\w+\s+)?code\b|\bcode\s*(?:is|:)`)

// near: wording a code sits next to.
var near = regexp.MustCompile(`(?i)\bcode\b|\bpasscode\b|\bOTP\b|\bPIN\b|\bverification\b|\bverify\b`)

// promo: a code for money off isn't a sign-in code.
var promo = regexp.MustCompile(`(?i)promo|discount|voucher|coupon|% off|\boff\b|offer|sale|gift card|referral|redeem|save \d`)

// notNumbers: words that make a number something else.
var notNumbers = regexp.MustCompile(`(?i)(?:order|ref|reference|invoice|account|acct|number|no\.|#|tel|phone|call|mobile|booking|ticket|case|customer|member|tracking|postcode|flight|seat|room|£|\$|€)\s*:?\s*$`)

var (
	// 4–8 digits, maybe split "123 456" / "123-456", maybe with a letter
	// prefix "G-123456" (Google), standing alone.
	digits = regexp.MustCompile(`(?:^|[^\w£$€.,:/#@-])(?:[A-Z]{1,3}-)?(\d{3}[ -]\d{3}|\d{4,8})(?:$|[^\w%:/@-])`)
	// Letters and digits mixed, 5–8, upper case: "F7K2Q", "AB12CD34".
	alnum = regexp.MustCompile(`(?:^|[^\w.-])([A-Z0-9]{5,8})(?:$|[^\w@.-]|\.(?:\s|$))`)
)

// wordCode: promo codes are a word and a number ("SAVE30", "PUURE10",
// "APP10"); sign-in codes are random ("F7K2Q").
var wordCode = regexp.MustCompile(`^[A-Z]{3,}\d{1,3}$`)

// Looks says whether an email might hold a code, from its subject and
// preview alone: a cheap first look before fetching the whole message.
func Looks(subject, preview string) bool {
	return strong.MatchString(subject+"\n"+preview) && !promo.MatchString(subject)
}

// Find returns the sign-in code in an email, or "" if it has none.
func Find(subject, text string) string {
	head := subject + "\n" + firstChars(text, 1500)
	if !strong.MatchString(head) || promo.MatchString(subject) {
		return ""
	}
	// The subject often has the code itself: "123456 is your code".
	if c := best(subject, true); c != "" {
		return c
	}
	return best(firstChars(text, 3000), false)
}

// best picks the candidate closest to code wording in s, if close enough.
func best(s string, isSubject bool) string {
	type cand struct {
		code  string
		start int
		end   int
	}
	var cands []cand
	for _, m := range digits.FindAllStringSubmatchIndex(s, -1) {
		c := s[m[2]:m[3]]
		code := strings.NewReplacer(" ", "", "-", "").Replace(c)
		if len(code) == 4 && (strings.HasPrefix(code, "19") || strings.HasPrefix(code, "20")) {
			continue // a year
		}
		cands = append(cands, cand{code, m[2], m[3]})
	}
	for _, m := range alnum.FindAllStringSubmatchIndex(s, -1) {
		c := s[m[2]:m[3]]
		if !mixed(c) || wordCode.MatchString(c) {
			continue
		}
		cands = append(cands, cand{c, m[2], m[3]})
	}
	words := near.FindAllStringIndex(s, -1)
	if len(words) == 0 && !isSubject {
		return ""
	}
	bestCode, bestDist := "", 1<<30
	for _, c := range cands {
		before := s[max(0, c.start-40):c.start]
		if notNumbers.MatchString(before) {
			continue
		}
		// Promo wording right around it: a discount code.
		around := s[max(0, c.start-60):min(len(s), c.end+40)]
		if promo.MatchString(around) {
			continue
		}
		if inURL(s, c.start) {
			continue
		}
		dist := 1 << 30
		for _, w := range words {
			d := 0
			switch {
			case w[1] <= c.start:
				d = c.start - w[1]
			case w[0] >= c.end:
				d = w[0] - c.end
			}
			dist = min(dist, d)
		}
		if isSubject && len(words) == 0 {
			dist = 50 // "123456 is your Shop sign-in code" has "code"; this is "Your sign-in: 123456"
		}
		if dist < bestDist {
			bestCode, bestDist = c.code, dist
		}
	}
	if bestDist > 80 {
		return ""
	}
	return bestCode
}

// mixed: a code like "F7K2Q" has both letters and digits (all-letter words
// like "VERIFY" and all-digit ones are handled elsewhere).
func mixed(s string) bool {
	letters, nums := 0, 0
	for _, r := range s {
		if unicode.IsDigit(r) {
			nums++
		} else {
			letters++
		}
	}
	return letters > 0 && nums > 0
}

func inURL(s string, at int) bool {
	start := strings.LastIndexAny(s[:at], " \n\t<>\"'")
	word := s[start+1 : at]
	return strings.Contains(word, "://") || strings.HasPrefix(word, "www.") || strings.Contains(word, "/")
}

func firstChars(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
