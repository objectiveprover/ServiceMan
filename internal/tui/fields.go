package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"
)

// newTextInput returns a textinput with Emacs-flavoured bindings
// (C-a C-e C-f C-b M-f M-b C-k C-u C-w M-d C-d are bubbles defaults; C-y yanks).
func newTextInput() textinput.Model {
	ti := textinput.New()
	ti.Prompt = ""
	ti.KeyMap.Paste = key.NewBinding(key.WithKeys("ctrl+y"))
	ti.KeyMap.NextSuggestion = key.NewBinding(key.WithDisabled())
	ti.KeyMap.PrevSuggestion = key.NewBinding(key.WithDisabled())
	ti.KeyMap.AcceptSuggestion = key.NewBinding(key.WithDisabled())
	return ti
}

// inputLead starts a choice field's line of options, under its label
// (accent while the field is focused).
func inputLead(focused bool) string {
	return fieldLine(focused).Render("└ ")
}

// fieldLine is the style of a form field's └, ❯ and bottom line: accent
// while the field is focused, dim otherwise.
func fieldLine(focused bool) lipgloss.Style {
	if focused {
		return sKey
	}
	return sDim
}

// fieldWidth is how wide a field's bottom line is in a column w wide: the
// whole column minus the gutter, so on a full-width form it lines up with
// the screen's other rules.
func fieldWidth(w int) int { return max(12, w-len(gutter)) }

// fieldBottom is the ─ line under every form field.
func fieldBottom(focused bool, w int) string {
	return trunc(gutter+fieldLine(focused).Render(strings.Repeat("─", fieldWidth(w))), w)
}

// inputTextWidth is the textinput Width for a text field in a column w wide,
// so the input (after "❯ ", plus a cell for the cursor) ends where the
// bottom line does.
func inputTextWidth(w int) int { return fieldWidth(w) - 3 }

// textField renders a text input as three lines in a column w wide: its name
// (and gray description), the input after a ❯, and the ─ line under it.
// mark is the gutter (cursorMark), label the styled name.
//
//	▸ Name  The unit's name
//	  ❯ myapp
//	  ──────────────────────
func textField(mark, label, help, input string, focused bool, w int) []string {
	head := mark + label
	if help != "" {
		head += "  " + sDesc.Render(help)
	}
	return []string{trunc(head, w), trunc(gutter+fieldLine(focused).Render("❯ ")+input, w), fieldBottom(focused, w)}
}

// choiceLabel is how a selector shows an option value: "on-failure" reads
// "On failure".
func choiceLabel(v string) string {
	v = strings.ReplaceAll(v, "-", " ")
	if v == "" {
		return v
	}
	return strings.ToUpper(v[:1]) + v[1:]
}
