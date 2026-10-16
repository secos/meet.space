package branding_test

import (
	"context"
	"regexp"
	"slices"
	"sort"
	"testing"

	"github.com/jackc/pgerrcode"

	"meet.space/internal/branding"
	"meet.space/internal/testkit"
)

// rule10Hex is Security Rule 10's pattern, copied from development-plan.md.
// Go's $ (without the m flag) matches only at the end of the text, so a
// trailing newline does not match.
var rule10Hex = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// rule10Looks is Rule 10's allowlist, copied from development-plan.md (not
// taken from branding.Looks, so widening the package's list fails here).
var rule10Looks = []string{"clean", "lively", "confetti", "poster"}

// P0-SECURITY-16
// Rule 10: a color reaches a <style> block only if it matches
// ^#[0-9a-fA-F]{6}$. These are the cases most likely to slip through a
// hand-written check: CSS injection, other color syntaxes, near-misses.
func TestRule10_ValidHex(t *testing.T) {
	for _, c := range []struct {
		in   string
		want bool
	}{
		{"#2563eb", true},
		{"#2563EB", true},
		{"#000000", true},
		{"#ffffff", true},
		{"#AbCdEf", true},
		{"#09afAF", true},

		{"", false},
		{"#", false},
		{"2563eb", false},
		{"#2563e", false},
		{"#2563ebb", false},
		{"#2563eb00", false},
		{"#fff", false},
		{"#ffff", false},
		{"#2563eg", false},
		{"#2563e ", false},
		{" #2563eb", false},
		{"#2563eb ", false},
		{"#2563eb\n", false},
		{"#2563eb\x00", false},
		{"##2563eb", false},
		{"＃2563eb", false},
		{"#２５６３eb", false},
		{"#2563eb;}", false},
		{"#2563eb}</style><script>alert(1)</script>", false},
		{"red", false},
		{"rgb(0,0,0)", false},
		{"var(--x)", false},
		{"#2563eb !important", false},
		{"#0x1234", false},
		{"#-12345", false},
		{"#+12345", false},
	} {
		if got := branding.ValidHex(c.in); got != c.want {
			t.Errorf("ValidHex(%q) = %v, want %v", c.in, got, c.want)
		}
		if rule10Hex.MatchString(c.in) != c.want {
			t.Fatalf("table case %q disagrees with Rule 10's pattern", c.in)
		}
	}
}

// P0-SECURITY-16
// ValidHex accepts exactly the strings Rule 10's pattern matches.
func FuzzRule10_ValidHex(f *testing.F) {
	for _, s := range []string{
		"#2563eb", "#2563EB", "#2563e", "#2563ebb", "#2563eb\n", "2563eb#",
		"#gggggg", "#GGGGGG", "#ÿÿÿ", "#\xff\xff\xff\xff\xff\xff", "#٠١٢٣٤٥", "",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if got, want := branding.ValidHex(s), rule10Hex.MatchString(s); got != want {
			t.Fatalf("ValidHex(%q) = %v, Rule 10 pattern says %v", s, got, want)
		}
	})
}

// P0-SECURITY-16, P0-LAYOUT-19 (default #2563eb)
// The platform default brand color (design.md 2.1) passes Rule 10 like any
// group's color, since it is written into the same <style> block.
func TestRule10_DefaultBrandIsValidHex(t *testing.T) {
	if branding.DefaultBrand != "#2563eb" {
		t.Errorf("DefaultBrand = %q, design.md 2.1 says #2563eb", branding.DefaultBrand)
	}
	if !branding.ValidHex(branding.DefaultBrand) {
		t.Errorf("DefaultBrand %q fails Rule 10", branding.DefaultBrand)
	}
}

// P0-SECURITY-15 (data-look is one of the four looks; this is the check it relies on)
// Rule 10: the look must be one of clean, lively, confetti, poster. The
// match is exact and case-sensitive, as the groups.look CHECK constraint is.
func TestRule10_LookAllowlist(t *testing.T) {
	got := slices.Clone(branding.Looks)
	want := slices.Clone(rule10Looks)
	sort.Strings(got)
	sort.Strings(want)
	if !slices.Equal(got, want) {
		t.Errorf("branding.Looks = %q, Rule 10 allows %q", branding.Looks, rule10Looks)
	}
	for _, l := range rule10Looks {
		if !branding.ValidLook(l) {
			t.Errorf("ValidLook(%q) = false", l)
		}
	}
	for _, l := range []string{
		"", "Clean", "CLEAN", "Lively", "Confetti", "POSTER",
		" clean", "clean ", "clean\n", "clean\x00", "cléan", "clean,lively",
		"noticeboard", "program", "default", "none",
		`clean" onload="alert(1)`, "clean]{} body{", "../clean",
	} {
		if branding.ValidLook(l) {
			t.Errorf("ValidLook(%q) = true", l)
		}
	}
}

// P0-SECURITY-15
// ValidLook accepts exactly the four names, whatever the input.
func FuzzRule10_ValidLook(f *testing.F) {
	for _, s := range append(slices.Clone(rule10Looks), "", "Clean", "clean ", "posterr", "confett") {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if got, want := branding.ValidLook(s), slices.Contains(rule10Looks, s); got != want {
			t.Fatalf("ValidLook(%q) = %v, want %v", s, got, want)
		}
	})
}

// P0-SECURITY-15, P0-LAYOUT-20 (platform pages are clean)
// What reaches data-look is always allowlisted (Rule 10). A valid look
// passes through unchanged; anything else becomes Clean, the default look
// for every group and the platform (decisions.md "Visual design";
// groups.look DEFAULT 'clean').
func TestRule10_LookOrClean(t *testing.T) {
	for _, l := range rule10Looks {
		if got := branding.LookOrClean(l); got != l {
			t.Errorf("LookOrClean(%q) = %q", l, got)
		}
	}
	for _, l := range []string{"", "Lively", "noticeboard", `poster" x="`, "poster "} {
		if got := branding.LookOrClean(l); got != branding.LookClean {
			t.Errorf("LookOrClean(%q) = %q, want %q", l, got, branding.LookClean)
		}
	}
	if branding.LookClean != "clean" {
		t.Errorf("LookClean = %q", branding.LookClean)
	}
}

// P0-SECURITY-15
// For any input, LookOrClean returns an allowlisted look: the input itself
// when it is valid, Clean otherwise.
func FuzzRule10_LookOrClean(f *testing.F) {
	for _, s := range append(slices.Clone(rule10Looks), "", "Clean", "x") {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		got := branding.LookOrClean(s)
		if !slices.Contains(rule10Looks, got) {
			t.Fatalf("LookOrClean(%q) = %q, not allowlisted", s, got)
		}
		if slices.Contains(rule10Looks, s) && got != s {
			t.Fatalf("LookOrClean(%q) = %q, want it unchanged", s, got)
		}
		if !slices.Contains(rule10Looks, s) && got != "clean" {
			t.Fatalf("LookOrClean(%q) = %q, want clean", s, got)
		}
	})
}

// P0-SCHEMA-19 (the validator's side; migrations/tables_test.go has the
// schema's side)
// Rule 10 enforces the look twice, "by a CHECK constraint and by the
// handler". The two must agree: every name ValidLook accepts is accepted by
// the real groups.look constraint, and every name it refuses is refused
// there, by groups_look_check itself and not some other CHECK.
func TestRule10_LookAllowlistMatchesCheckConstraint(t *testing.T) {
	db := testkit.NewDB(t)
	ctx := context.Background()
	candidates := append(slices.Clone(rule10Looks),
		"Clean", "CLEAN", "clean ", " clean", "noticeboard", "program", "")
	for i, look := range candidates {
		tx, err := db.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, err = tx.Exec(ctx,
			`INSERT INTO groups (id, slug, name, timezone, look)
			 VALUES (gen_random_uuid(), $1, 'Look probe', 'UTC', $2)`,
			"look-probe-"+string(rune('a'+i)), look)
		_ = tx.Rollback(ctx)

		if branding.ValidLook(look) {
			if err != nil {
				t.Errorf("look %q: ValidLook accepts it but the insert failed: %v", look, err)
			}
			continue
		}
		if !testkit.WantPgError(t, err, pgerrcode.CheckViolation, "groups_look_check") {
			t.Errorf("look %q: ValidLook refuses it; the groups.look CHECK must too", look)
		}
	}
}
