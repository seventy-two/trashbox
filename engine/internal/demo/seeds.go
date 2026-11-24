package demo

import (
	"encoding/base64"
	"strconv"
	"strings"
	"time"
)

// The demo's mail: invented people, shops and places, on example domains.

type seed struct {
	ago     time.Duration
	name    string
	addr    string
	subject string
	text    string
	html    string
	seen    bool
	flagged bool
	attach  bool
}

const (
	minute = time.Minute
	hour   = time.Hour
	day    = 24 * time.Hour
)

// allotmentImg is the allotment newsletter's remote picture, blocked until
// asked for (so the demo shows the "pictures blocked" bar).
var allotmentImg = "https://images.example.com/allotment/header.jpg"

func allotment() string {
	return `<html><body style="margin:0;background:#f3efe6;font-family:Georgia,serif">
<table width="100%" cellpadding="0" cellspacing="0"><tr><td align="center">
<table width="560" cellpadding="24" style="background:#fffdf8">
<tr><td><img src="` + allotmentImg + `" width="512" alt="Allotment Weekly"></td></tr>
<tr><td><h1 style="color:#4a5a2a;font-weight:normal">Leeks, at last</h1>
<p style="color:#333;line-height:1.6">The frost held off long enough this week for the leeks to thicken up nicely.
Plot 14 is still winning the marrow argument, and the shed roof has been patched (again).</p>
<p style="color:#333;line-height:1.6">This Saturday: seed swap at the gate from 10 until the tea runs out.</p>
<p><a href="https://allotment.example.com/swap" style="background:#4a5a2a;color:#fff;padding:10px 18px;text-decoration:none">See what's on the table</a></p>
</td></tr></table></td></tr></table></body></html>`
}

func svg(s string) string {
	return "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(s))
}

// The night-train hero: hills at dusk, a lit train crossing a viaduct.
var heroSVG = svg(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 600 260">
<defs><linearGradient id="sky" x1="0" y1="0" x2="0" y2="1">
<stop offset="0" stop-color="#1b2a4a"/><stop offset=".55" stop-color="#7a4a7a"/><stop offset="1" stop-color="#f0a060"/></linearGradient></defs>
<rect width="600" height="260" fill="url(#sky)"/>
<circle cx="470" cy="150" r="34" fill="#ffd9a0" opacity=".9"/>
<g fill="#fff" opacity=".8"><circle cx="60" cy="30" r="1.4"/><circle cx="140" cy="60" r="1"/><circle cx="230" cy="22" r="1.2"/><circle cx="330" cy="48" r="1"/><circle cx="400" cy="18" r="1.4"/><circle cx="540" cy="40" r="1"/></g>
<path d="M0 190 L80 150 L160 175 L250 130 L340 170 L430 140 L520 175 L600 150 L600 260 L0 260Z" fill="#3a2f4f"/>
<path d="M0 215 L100 185 L200 205 L310 180 L420 210 L520 190 L600 205 L600 260 L0 260Z" fill="#241d33"/>
<rect x="0" y="196" width="600" height="6" fill="#151020"/>
<g fill="#151020"><rect x="30" y="202" width="10" height="58"/><rect x="130" y="202" width="10" height="58"/><rect x="230" y="202" width="10" height="58"/><rect x="330" y="202" width="10" height="58"/><rect x="430" y="202" width="10" height="58"/><rect x="530" y="202" width="10" height="58"/></g>
<g><rect x="150" y="178" width="250" height="18" rx="4" fill="#151020"/>
<g fill="#ffcf70">` + windows() + `</g></g>
</svg>`)

func windows() string {
	var b strings.Builder
	for x := 158; x < 392; x += 14 {
		b.WriteString(`<rect x="` + strconv.Itoa(x) + `" y="182" width="9" height="6" rx="1"/>`)
	}
	return b.String()
}

func tile(c1, c2, shape string) string {
	return svg(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 160 100">
<defs><linearGradient id="g" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="` + c1 + `"/><stop offset="1" stop-color="` + c2 + `"/></linearGradient></defs>
<rect width="160" height="100" fill="url(#g)"/>` + shape + `</svg>`)
}

func nightTrain() string {
	card := func(img, kicker, title, text string) string {
		return `<td width="33%" valign="top" style="padding:0 8px">
<img src="` + img + `" style="display:block;width:100%;border-radius:6px">
<p style="margin:12px 0 4px;font:600 11px/1.2 Arial,sans-serif;letter-spacing:.08em;color:#b0603a;text-transform:uppercase">` + kicker + `</p>
<p style="margin:0 0 6px;font:bold 16px/1.3 Georgia,serif;color:#1f1a17">` + title + `</p>
<p style="margin:0;font:14px/1.5 Georgia,serif;color:#555">` + text + `</p></td>`
	}
	return `<html><body style="margin:0;background:#efe9e1">
<table width="100%" cellpadding="0" cellspacing="0"><tr><td align="center" style="padding:24px 12px">
<table cellpadding="0" cellspacing="0" style="width:100%;max-width:600px;background:#fffaf3;border-radius:10px;overflow:hidden">
<tr><td style="padding:18px 28px;font:bold 13px Arial,sans-serif;letter-spacing:.2em;color:#1f1a17">SLOW TRAVEL WEEKLY <span style="float:right;font-weight:normal;letter-spacing:0;color:#999">Issue 212</span></td></tr>
<tr><td><img src="` + heroSVG + `" style="display:block;width:100%"></td></tr>
<tr><td style="padding:28px 28px 8px">
<h1 style="margin:0 0 12px;font:bold 30px/1.2 Georgia,serif;color:#1f1a17">The quiet return of the night train</h1>
<p style="margin:0 0 14px;font:17px/1.6 Georgia,serif;color:#3a3330">Go to sleep in one country, wake up in another. After years of cuts, sleeper routes are coming back across Europe, and this winter there are more beds on the rails than at any time since the nineties.</p>
<p style="margin:0 0 22px;font:17px/1.6 Georgia,serif;color:#3a3330">We spent a fortnight on six of them, from a cosy couchette in the Alps to a dining car that still serves soup in proper bowls.</p>
<a href="https://slowtravel.example.com/night-trains" style="display:inline-block;background:#b0603a;color:#fff;font:bold 14px Arial,sans-serif;padding:12px 22px;border-radius:6px;text-decoration:none">Read the full story</a>
</td></tr>
<tr><td style="padding:30px 20px 8px"><table width="100%" cellpadding="0" cellspacing="0"><tr>` +
		card(tile("#2f6f6a", "#a8d5c2", `<path d="M0 80 L40 50 L70 70 L110 35 L160 75 L160 100 L0 100Z" fill="#1f4a46"/>`), "Routes", "Six sleepers worth booking", "From the Highlands to the Adriatic, with prices.") +
		card(tile("#6a3f7a", "#e8b0c0", `<circle cx="110" cy="40" r="18" fill="#ffe2c0"/><rect x="0" y="72" width="160" height="28" fill="#3d2448"/>`), "Packing", "What to bring for a couchette", "Earplugs, slippers and one very good book.") +
		card(tile("#b0603a", "#f3c98b", `<rect x="40" y="38" width="80" height="44" rx="6" fill="#7a3a1f"/><rect x="50" y="46" width="60" height="16" rx="3" fill="#ffe7b0"/>`), "Food", "Dinner at 80 mph", "The last proper dining cars, ranked.") +
		`</tr></table></td></tr>
<tr><td style="padding:28px;font:12px/1.6 Arial,sans-serif;color:#999;border-top:1px solid #eee">You get this because you signed up at slowtravel.example.com. <a href="https://slowtravel.example.com/unsubscribe" style="color:#999">Unsubscribe</a></td></tr>
</table></td></tr></table></body></html>`
}

const receipt = `<html><body style="font-family:Arial,sans-serif;color:#222">
<h2 style="margin:0 0 12px">Thanks for your order</h2>
<p>Order <b>KB-48213</b> is on its way.</p>
<table cellpadding="6" style="border-collapse:collapse">
<tr style="background:#eee"><th align="left">Item</th><th align="right">Price</th></tr>
<tr><td>Kettle, brushed steel</td><td align="right">£34.00</td></tr>
<tr><td>Descaler (2 pack)</td><td align="right">£4.50</td></tr>
<tr><td><b>Total</b></td><td align="right"><b>£38.50</b></td></tr></table>
<p style="color:#666;font-size:12px">Kettle &amp; Bean Ltd · this is an invented shop</p></body></html>`

const energy = `<html><body style="margin:0;font-family:Segoe UI,Arial,sans-serif;background:#f4f6f8">
<table width="100%" cellpadding="0" cellspacing="0"><tr><td align="center" style="padding:20px">
<table width="560" cellpadding="0" cellspacing="0" style="background:#fff;border-radius:8px">
<tr><td style="background:#1d6b4f;color:#fff;padding:18px 24px;font-size:20px;font-weight:600">Cloudberry Energy</td></tr>
<tr><td style="padding:24px">
<p style="font-size:16px;margin:0 0 16px">Your November statement is ready.</p>
<table width="100%" cellpadding="8" style="border-collapse:collapse;font-size:14px">
<tr style="border-bottom:1px solid #eee"><td>Electricity</td><td align="right">£41.12</td></tr>
<tr style="border-bottom:1px solid #eee"><td>Gas</td><td align="right">£38.70</td></tr>
<tr><td><b>Paid by Direct Debit on 1 December</b></td><td align="right"><b>£79.82</b></td></tr></table>
<p style="font-size:13px;color:#666;margin:18px 0 0">You used 12% less than this time last year. Nice.</p>
</td></tr></table></td></tr></table></body></html>`

const records = `<html style="background:#111"><body style="margin:0;background:#111;color:#eee;font-family:Helvetica,Arial,sans-serif">
<table width="100%" cellpadding="0" cellspacing="0"><tr><td align="center" style="padding:30px">
<table cellpadding="0" cellspacing="0" style="width:100%;max-width:520px">
<tr><td style="font-size:12px;letter-spacing:.3em;color:#888">FIELDNOTE RECORDS</td></tr>
<tr><td style="padding:24px 0"><div style="height:260px;border-radius:4px;background:radial-gradient(circle at 50% 50%,#111 0 18px,#d94f30 19px 60px,#1a1a1a 61px 62px,#222 63px 130px,#151515 131px)"></div></td></tr>
<tr><td style="font-size:26px;font-weight:bold">Low Tide — 10th anniversary pressing</td></tr>
<tr><td style="font-size:15px;line-height:1.6;color:#bbb;padding:10px 0 20px">Half-speed master, tangerine vinyl, a new sleeve and a 12-page booklet. 500 copies, shipping in February.</td></tr>
<tr><td><a href="https://fieldnote.example/low-tide" style="color:#111;background:#eee;padding:10px 20px;text-decoration:none;font-weight:bold">Pre-order</a></td></tr>
</table></td></tr></table></body></html>`

func accounts() []account {
	return []account{{
		id: "sam@example.com", kind: "outlook", codes: true,
		inbox: []seed{
			{ago: 4 * minute, name: "Fernhill Bank", addr: "no-reply@fernhill.example", subject: "Your sign-in code",
				text: "Your Fernhill sign-in code is 482913.\n\nIt expires in 10 minutes. If this wasn't you, you can ignore this email."},
			{ago: 40 * minute, name: "Priya Shah", addr: "priya@example.org", subject: "Sunday roast?",
				text: "Are you still up for Sunday? I'm thinking the place by the river, about 1pm.\n\nSam's coming too, and might bring the dog.\n\nP"},
			{ago: 75 * minute, name: "Slow Travel Weekly", addr: "letters@slowtravel.example.com", subject: "The quiet return of the night train", html: nightTrain()},
			{ago: 2*hour + 10*minute, name: "Cloudberry Energy", addr: "statements@cloudberry.example", subject: "Your November statement", html: energy},
			{ago: 3 * hour, name: "Allotment Weekly", addr: "news@allotment.example.com", subject: "Leeks, at last", html: allotment()},
			{ago: 7 * hour, name: "Kettle & Bean", addr: "orders@kettlebean.example", subject: "Order KB-48213 is on its way", html: receipt, seen: true, attach: true},
			{ago: 9 * hour, name: "Harbourside Cinema", addr: "tickets@harbourside.example", subject: "Your tickets for Friday", seen: true,
				html: `<div style="font-family:Segoe UI,sans-serif"><h2>Friday, 19:40, Screen 3</h2><p>Seats <b>F7, F8</b>. Show this email at the door.</p></div>`},
			{ago: 26 * hour, name: "Tom Okafor", addr: "tom.okafor@example.net", subject: "Re: bike lights", seen: true, flagged: true,
				text: "Found them. They were in the hall cupboard behind the hoover, obviously.\n\nI'll drop them round on Thursday, and I'll bring the pump back while I'm at it.\n\nTom\n\nOn Sun, Sam wrote:\n> Did you ever find the bike lights?\n> I can't find mine anywhere and it's getting dark at four."},
			{ago: 30 * hour, name: "Westway Trains", addr: "tickets@westway.example", subject: "Your e-ticket: Bristol Temple Meads → Manchester", seen: true, attach: true,
				text: "Your e-ticket is attached.\n\nSaturday 5 December, 08:27, coach C, seat 42 (window, forward facing).\n\nHave a good trip."},
			{ago: 2*day + 3*hour, name: "Jo Lindqvist", addr: "jo@lindqvist.example", subject: "Holiday photos 📷", seen: true, attach: true,
				text: "Finally sorted them! The one of the goat on the wall is my favourite.\n\nJo x"},
			{ago: 3 * day, name: "Council Tax", addr: "billing@council.example.gov", subject: "Your new bill", seen: true,
				text: "Your bill for next year is ready to view online.\n\nhttps://council.example.gov/bills"},
			{ago: 4*day + 5*hour, name: "Greenleaf Vets", addr: "reminders@greenleaf.example", subject: "Biscuit's booster is due", seen: true,
				text: "Biscuit is due a booster vaccination this month. Book online or call the practice.\n\nGreenleaf Vets"},
			{ago: 6 * day, name: "Ravi Patel", addr: "ravi.p@example.com", subject: "Five-a-side Thursday", seen: true,
				text: "Pitch 2 at 8. We're one short, can you bring Dan?"},
			{ago: 80 * day, name: "Priya Shah", addr: "priya@example.org", subject: "Photos from the walk", seen: true, attach: true,
				text: "A few from the top of the hill. The fog did lift in the end!"},
			{ago: 95 * day, name: "Railcard", addr: "renewals@railcard.example", subject: "Time to renew", seen: true,
				text: "Your railcard runs out next month."},
		},
		junk: []seed{
			{ago: 2 * hour, name: "Prize Desk", addr: "winner@prizes.example", subject: "You've won (again)", text: "Claim now!"},
		},
	}, {
		id: "sam.rivers@example.org", kind: "gmail",
		inbox: []seed{
			{ago: 20 * minute, name: "Climbing Club", addr: "hello@climbing.example", subject: "Wednesday session moved to 7pm",
				text: "The wall's being reset, so we're starting an hour later this week."},
			{ago: 5 * hour, name: "Fieldnote Records", addr: "shop@fieldnote.example", subject: "Low Tide is back on vinyl", html: records},
			{ago: 9 * hour, name: "Mum", addr: "mum@example.com", subject: "Christmas", seen: true,
				text: "Let me know about Christmas soon please! Dad wants to book the train."},
			{ago: 2 * day, name: "Riverside 5k", addr: "results@riverside5k.example", subject: "Your result: 24:31", seen: true,
				text: "Well done! 24:31, a new best by 18 seconds. 112th of 340."},
			{ago: 4 * day, name: "Bookshop on the Corner", addr: "shop@books.example", subject: "Your reservation is in", seen: true,
				text: "The book you reserved has arrived. We'll keep it behind the counter for two weeks."},
			{ago: 8 * day, name: "Smile Dental", addr: "appointments@smile.example", subject: "Appointment reminder", seen: true,
				text: "This is a reminder of your check-up on Tuesday at 9:10."},
		},
		junk: []seed{
			{ago: 6 * hour, name: "Crypto Wins", addr: "news@cryptowins.example", subject: "Last chance!!!", text: "Double your money today."},
		},
	}, {
		id: "sam_1998@example.net", kind: "outlook",
		inbox: []seed{
			{ago: 50 * minute, name: "Pixel Arcade", addr: "deals@pixelarcade.example", subject: "Weekend sale: retro bundles",
				html: `<div style="font-family:Verdana,sans-serif;background:#20123a;color:#ffe066;padding:24px;text-align:center"><h1 style="margin:0">RETRO WEEKEND</h1><p style="color:#fff">Bundles of 8-bit classics, this weekend only.</p></div>`},
			{ago: 2*day + 6*hour, name: "Forum Digest", addr: "digest@forum.example", subject: "This week in Old Cars", seen: true,
				text: "Top threads: carburettor woes, a barn find, and the eternal question of which wax."},
		},
	}}
}
