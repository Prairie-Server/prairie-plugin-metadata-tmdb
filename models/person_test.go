package models

import "testing"

func TestPersonKindString(t *testing.T) {
	t.Parallel()
	tests := []struct {
		kind PersonKind
		want string
	}{
		{PersonKindActor, "Actor"},
		{PersonKindDirector, "Director"},
		{PersonKindWriter, "Writer"},
		{PersonKindProducer, "Producer"},
		{PersonKindGuestStar, "GuestStar"},
		{PersonKindComposer, "Composer"},
		{PersonKind(0), "Unknown"},
	}
	for _, tt := range tests {
		if got := tt.kind.String(); got != tt.want {
			t.Fatalf("%v.String() = %q, want %q", tt.kind, got, tt.want)
		}
	}
}

func TestPersonKindFromJob(t *testing.T) {
	t.Parallel()
	tests := []struct {
		job  string
		want PersonKind
	}{
		{"Director", PersonKindDirector},
		{"writer", PersonKindWriter},
		{"Screenplay", PersonKindWriter},
		{"Story", PersonKindWriter},
		{"Novel", PersonKindWriter},
		{"Composer", PersonKindComposer},
		{"Original Music Composer", PersonKindComposer},
		{"Music", PersonKindComposer},
		{"Producer", PersonKindProducer},
		{"Executive Producer", PersonKindProducer},
	}
	for _, tt := range tests {
		if got := PersonKindFromJob(tt.job); got != tt.want {
			t.Fatalf("PersonKindFromJob(%q) = %v, want %v", tt.job, got, tt.want)
		}
	}
}
