package store

import (
	"strings"
	"testing"
)

func TestConfigRoundTripAndSecrets(t *testing.T) {
	t.Setenv("TRASHBOX_DATA", t.TempDir())
	s, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Load()
	if err != nil || len(c.Accounts) != 0 {
		t.Fatalf("empty load: %+v %v", c, err)
	}
	c.OutlookClientID = "cid"
	c.Add(Account{Kind: Outlook, Address: "Me@Outlook.com"})
	c.Add(Account{Kind: Outlook, Address: "me@outlook.com"}) // same account again
	c.Add(Account{Kind: Gmail, Address: "me@gmail.com"})
	if err := s.Save(c); err != nil {
		t.Fatal(err)
	}
	c2, err := s.Load()
	if err != nil || c2.OutlookClientID != "cid" || len(c2.Accounts) != 2 {
		t.Fatalf("reload %+v %v", c2, err)
	}

	a, err := c2.Find(Outlook, "")
	if err != nil || a.Address != "me@outlook.com" {
		t.Fatalf("find %+v %v", a, err)
	}
	if err := s.SetSecret(a, "refresh-token"); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Secret(a); err != nil || got != "refresh-token" {
		t.Fatalf("secret %q %v", got, err)
	}
}

func TestFindAmbiguous(t *testing.T) {
	c := Config{Accounts: []Account{{Gmail, "a@gmail.com"}, {Gmail, "b@gmail.com"}}}
	if _, err := c.Find(Gmail, ""); err == nil || !strings.Contains(err.Error(), "-account") {
		t.Fatalf("want ambiguity error, got %v", err)
	}
	if a, err := c.Find(Gmail, "B@gmail.com"); err != nil || a.Address != "b@gmail.com" {
		t.Fatalf("%+v %v", a, err)
	}
	if _, err := c.Find(Outlook, ""); err == nil {
		t.Fatal("want none error")
	}
}
