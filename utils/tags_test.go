package utils

import "testing"

func TestNormalizeTagsCSV_Empty(t *testing.T) {
	csv, tags, err := NormalizeTagsCSV("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if csv != "" {
		t.Fatalf("expected empty csv, got %q", csv)
	}
	if len(tags) != 0 {
		t.Fatalf("expected 0 tags, got %d", len(tags))
	}
}

func TestNormalizeTagsCSV_Basic(t *testing.T) {
	csv, tags, err := NormalizeTagsCSV(" Games ,rpg,Games , avatar ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if csv != "Games,rpg,avatar" {
		t.Fatalf("csv mismatch: %q", csv)
	}
	if len(tags) != 3 {
		t.Fatalf("expected 3 tags, got %d", len(tags))
	}
}

func TestNormalizeTagsCSV_InvalidChars(t *testing.T) {
	if _, _, err := NormalizeTagsCSV("bad<tag>"); err == nil {
		t.Fatalf("expected error for invalid characters")
	}
}

func TestNormalizeTagsCSV_MaxLen(t *testing.T) {
	long := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" // 33 a's
	if _, _, err := NormalizeTagsCSV(long); err == nil {
		t.Fatalf("expected error for tag too long")
	}
}

func TestNormalizeTagsCSV_MaxTags(t *testing.T) {
	// 21 tags
	in := "t1,t2,t3,t4,t5,t6,t7,t8,t9,t10,t11,t12,t13,t14,t15,t16,t17,t18,t19,t20,t21"
	if _, _, err := NormalizeTagsCSV(in); err == nil {
		t.Fatalf("expected error for too many tags")
	}
}

func TestNormalizeTagsCSV_TotalLimit(t *testing.T) {
	// Build tags close to the limit; each tag is 50 chars -> total exceeds 512
	base := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" // 50 a's
	in := base
	for i := 0; i < 12; i++ { // 13*50 + 12 commas = 662 > 512
		in += "," + base
	}
	if _, _, err := NormalizeTagsCSV(in); err == nil {
		t.Fatalf("expected error for total too large")
	}
}

func TestNormalizeTagsCSV_AllowedChars(t *testing.T) {
	cases := []string{
		"game dev",
		"docs/v1",
		"alpha.beta",
		"snake_case",
		"dash-case",
	}
	for _, c := range cases {
		if _, _, err := NormalizeTagsCSV(c); err != nil {
			t.Fatalf("%q should be valid, got error: %v", c, err)
		}
	}
}

func TestNormalizeTagsCSV_UnicodeAccents(t *testing.T) {
	cases := []string{
		"ação",
		"maçã",
		"café",
		"Educação",
		"produção 2025",
	}
	for _, c := range cases {
		if _, _, err := NormalizeTagsCSV(c); err != nil {
			t.Fatalf("%q should be valid with Unicode accents, got error: %v", c, err)
		}
	}
}
