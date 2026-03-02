package forms

import "testing"

func TestHumanizePath(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/", "Home"},
		{"/contact", "Contact"},
		{"/contact-us", "Contact Us"},
		{"/api/feedback-form", "Feedback Form"},
		{"/a/b/c", "C"},
		{"/signup", "Signup"},
		{"/multi_word_path", "Multi Word Path"},
		{"/mix-and_match", "Mix And Match"},
		{"", "Home"},
		{"/trailing/", "Trailing"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := HumanizePath(tt.path)
			if got != tt.want {
				t.Errorf("HumanizePath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestHumanizeKey(t *testing.T) {
	tests := []struct {
		key  string
		want string
	}{
		{"first_name", "First Name"},
		{"lastName", "Last Name"},
		{"email", "Email"},
		{"phone_number", "Phone Number"},
		{"HTMLContent", "HTML Content"},
		{"userID", "User ID"},
		{"myURLParser", "My URL Parser"},
		{"simple", "Simple"},
		{"already_split-mixed", "Already Split Mixed"},
		{"IOError", "IO Error"},
		{"getHTTPSResponse", "Get HTTPS Response"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			got := HumanizeKey(tt.key)
			if got != tt.want {
				t.Errorf("HumanizeKey(%q) = %q, want %q", tt.key, got, tt.want)
			}
		})
	}
}
