package codes

import "testing"

func TestFind(t *testing.T) {
	cases := []struct {
		name, subject, text, want string
	}{
		{"code in subject", "052736 is your Shop sign-in code", "Hi, here you go.", "052736"},
		{"google prefix", "G-482913 is your Google verification code", "", "482913"},
		{"microsoft single-use", "Your single-use code",
			"Hi sam@example.com,\n\nWe received your request for a single-use code to use with your Microsoft account.\n\nYour single-use code is: 8241765\n\nIf you didn't request a code, you can safely ignore this email.", "8241765"},
		{"security code label", "Microsoft account security code", "Please use the following security code for the Microsoft account sa***@example.com.\n\nSecurity code: 3916\n\nThanks", "3916"},
		{"bank", "Your sign-in code", "Your Fernhill sign-in code is 482913.\n\nIt expires in 10 minutes.", "482913"},
		{"split digits", "Verify your email", "Enter this verification code: 123 456 to continue.", "123456"},
		{"steam guard", "Your Steam account: Access from new computer", "Here is the Steam Guard code you need to login to account x:\n\nF7K2Q\n\nThis email was generated", "F7K2Q"},
		{"otp word", "OTP", "Your OTP is 99812. Do not share it.", "99812"},
		{"code on its own line", "Confirm your sign-in", "Your login code:\n\n  774102\n\nThis code expires in 15 minutes.", "774102"},

		// Not codes.
		{"promo code", "Black Friday: 30% off", "Use code SAVE30 at checkout for 30% off everything.", ""},
		{"promo digits", "Your discount code", "Your discount code is 482913, enjoy 10% off your next order.", ""},
		{"order number", "Order #1119206 - Confirmation", "Thanks for your order. Order number: 1119206.", ""},
		{"no code talk", "Thanks for purchasing", "Your total was £34.00, order 48213, delivered 2026.", ""},
		{"price near code word", "Your code", "Unlock with code for just £1999 a month", ""},
		{"year", "Your verification", "Your verification for 2026 is done.", ""},
		{"phone", "Security code", "Call us on tel 0800 123 456 if you need help.", ""},
		{"sign-in link only", "Sign in to Slack", "Click here to sign in: https://slack.com/z/123456789/login?code=482913", ""},
		{"security alert no code", "Security alert", "A new sign-in to your account from Windows. If this was you, you don't need to do anything.", ""},
		{"tickets", "Your tickets for Friday", "Seats F7, F8. Booking ref ABC123. Screen 3.", ""},
		{"promo word code", "Your 10% off is waiting", "Don't miss out. Use code PUURE10 before it expires.", ""},
		{"promo word code, plain subject", "A little something", "Use code APP10 in the app before it expires.", ""},
		{"far away", "Your login code", "Hello,\n" + longPad() + "Reference 4421 for your records.", ""},
		{"far from the wording", "Your login code", "Your login code is below.\n" + longPad() + "We planted 4421 trees.", ""},
	}
	for _, c := range cases {
		if got := Find(c.subject, c.text); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func longPad() string {
	s := ""
	for i := 0; i < 10; i++ {
		s += "Lorem ipsum dolor sit amet, consectetur adipiscing elit.\n"
	}
	return s
}
