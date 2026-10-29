package convui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/partite-ai/partite-cli/internal/cmd/conversation/convapi"
)

// A2UI task forms whose components come from the basic catalog, shown in a
// terminal: drawn as text, and filled in one input at a time.

const basicCatalogID = "https://a2ui.org/specification/v0_9/basic_catalog.json"

type formSurface struct {
	Components []map[string]any `json:"components"`
}

// taskForm returns the surfaces of a task's a2ui presentation, when its
// components come from the basic catalog and a button can answer the task.
// completed is nil when the form has no surface for the completed task.
func taskForm(t convapi.Task) (open, completed *formSurface, ok bool) {
	var presentations []struct {
		Type    string `json:"type"`
		Content struct {
			CatalogID string       `json:"catalogId"`
			Open      *formSurface `json:"open"`
			Completed *formSurface `json:"completed"`
		} `json:"content"`
	}
	if t.ClientTask == nil || len(t.ClientTask.Presentations) == 0 || json.Unmarshal(t.ClientTask.Presentations, &presentations) != nil {
		return nil, nil, false
	}
	for _, p := range presentations {
		c := p.Content
		if p.Type != "a2ui" || c.CatalogID != basicCatalogID || c.Open == nil {
			continue
		}
		for _, comp := range c.Open.Components {
			if _, ok := eventOf(comp); ok {
				return c.Open, c.Completed, true
			}
		}
	}
	return nil, nil, false
}

// eventOf returns a button's event action, the one that answers the task.
func eventOf(c map[string]any) (map[string]any, bool) {
	if c["component"] != "Button" {
		return nil, false
	}
	action, _ := c["action"].(map[string]any)
	event, ok := action["event"].(map[string]any)
	return event, ok
}

// formState is a surface with its data model: the task input while the task is
// open, and its output once it is completed.
type formState struct {
	byID map[string]map[string]any
	data any
	// modalTriggers are the buttons that open a modal. The CLI draws a
	// modal's content in place, so it skips them.
	modalTriggers map[string]bool
}

func newForm(s *formSurface, data json.RawMessage) *formState {
	f := &formState{byID: map[string]map[string]any{}, modalTriggers: map[string]bool{}}
	for _, c := range s.Components {
		if id, ok := c["id"].(string); ok {
			f.byID[id] = c
		}
	}
	for _, c := range f.byID {
		if trigger, ok := c["trigger"].(string); ok && c["component"] == "Modal" {
			f.modalTriggers[trigger] = true
		}
	}
	if json.Unmarshal(data, &f.data) != nil || f.data == nil {
		f.data = map[string]any{}
	}
	return f
}

// pointer resolves a binding's path against base, the path of the list item
// a template is drawing. A path that starts with "/" is absolute.
func pointer(base, path string) string {
	switch {
	case strings.HasPrefix(path, "/"):
		return path
	case path == "":
		return base
	}
	return base + "/" + path
}

func pointerTokens(p string) []string {
	if p == "" || p == "/" {
		return nil
	}
	parts := strings.Split(strings.TrimPrefix(p, "/"), "/")
	for i, part := range parts {
		parts[i] = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
	}
	return parts
}

func (f *formState) get(p string) any {
	v := f.data
	for _, tok := range pointerTokens(p) {
		switch node := v.(type) {
		case map[string]any:
			v = node[tok]
		case []any:
			i, err := strconv.Atoi(tok)
			if err != nil || i < 0 || i >= len(node) {
				return nil
			}
			v = node[i]
		default:
			return nil
		}
	}
	return v
}

// set writes v at p, making the objects on the way that are missing.
func (f *formState) set(p string, v any) {
	toks := pointerTokens(p)
	if len(toks) == 0 {
		return
	}
	node := f.data
	for i, tok := range toks {
		last := i == len(toks)-1
		switch n := node.(type) {
		case map[string]any:
			if last {
				n[tok] = v
				return
			}
			if n[tok] == nil {
				n[tok] = map[string]any{}
			}
			node = n[tok]
		case []any:
			j, err := strconv.Atoi(tok)
			if err != nil || j < 0 || j >= len(n) {
				return
			}
			if last {
				n[j] = v
				return
			}
			node = n[j]
		default:
			return
		}
	}
}

// value evaluates a dynamic value: a literal, a binding to the data model, or
// a function call.
func (f *formState) value(v any, base string) any {
	if m, ok := v.(map[string]any); ok {
		if p, ok := m["path"].(string); ok {
			return f.get(pointer(base, p))
		}
		if name, ok := m["call"].(string); ok {
			args, _ := m["args"].(map[string]any)
			return f.call(name, args, base)
		}
	}
	return v
}

func (f *formState) text(v any, base string) string {
	return textOf(f.value(v, base))
}

func textOf(v any) string {
	switch v := v.(type) {
	case nil:
		return ""
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	case []any:
		parts := make([]string, len(v))
		for i, e := range v {
			parts[i] = textOf(e)
		}
		return strings.Join(parts, ", ")
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func numberOf(v any) (float64, bool) {
	switch v := v.(type) {
	case float64:
		return v, true
	case string:
		n, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return n, err == nil
	}
	return 0, false
}

var emailPattern = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

// call runs a function of the basic catalog. A function the CLI does not
// know returns nil.
func (f *formState) call(name string, args map[string]any, base string) any {
	arg := func(key string) any { return f.value(args[key], base) }
	inRange := func(n float64) bool {
		if min, ok := numberOf(arg("min")); ok && n < min {
			return false
		}
		if max, ok := numberOf(arg("max")); ok && n > max {
			return false
		}
		return true
	}
	switch name {
	case "required":
		switch v := arg("value").(type) {
		case nil:
			return false
		case string:
			return strings.TrimSpace(v) != ""
		case []any:
			return len(v) > 0
		}
		return true
	case "regex":
		re, err := regexp.Compile(textOf(arg("pattern")))
		return err == nil && re.MatchString(textOf(arg("value")))
	case "length":
		n := len([]rune(textOf(arg("value"))))
		if list, ok := arg("value").([]any); ok {
			n = len(list)
		}
		return inRange(float64(n))
	case "numeric":
		n, ok := numberOf(arg("value"))
		return ok && inRange(n)
	case "email":
		return emailPattern.MatchString(textOf(arg("value")))
	case "and", "or":
		values, _ := args["values"].([]any)
		if v, ok := arg("values").([]any); ok && values == nil {
			values = v
		}
		for _, v := range values {
			b := f.value(v, base) == true
			if name == "and" && !b {
				return false
			}
			if name == "or" && b {
				return true
			}
		}
		return name == "and"
	case "not":
		return arg("value") != true
	case "formatString":
		return f.interpolate(textOf(arg("value")), base)
	case "formatNumber", "formatCurrency":
		n, ok := numberOf(arg("value"))
		if !ok {
			return textOf(arg("value"))
		}
		decimals := -1
		if name == "formatCurrency" {
			decimals = 2
		}
		if d, ok := numberOf(arg("decimals")); ok {
			decimals = int(d)
		}
		grouping := arg("grouping") != false
		s := formatNumber(n, decimals, grouping)
		if name == "formatCurrency" {
			return currencyPrefix(textOf(arg("currency"))) + s
		}
		return s
	case "formatDate":
		return formatDate(textOf(arg("value")), textOf(arg("format")))
	case "pluralize":
		n, _ := numberOf(arg("value"))
		for _, c := range []struct {
			key string
			ok  bool
		}{{"zero", n == 0}, {"one", n == 1}, {"two", n == 2}} {
			if _, set := args[c.key]; set && c.ok {
				return textOf(arg(c.key))
			}
		}
		return textOf(arg("other"))
	}
	return nil
}

// interpolate fills in the ${path} expressions of a formatString value.
// Function calls inside it are left as they are.
func (f *formState) interpolate(s, base string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		switch {
		case strings.HasPrefix(s[i:], `\${`):
			b.WriteString("${")
			i += 3
		case strings.HasPrefix(s[i:], "${"):
			end := strings.IndexByte(s[i:], '}')
			if end < 0 {
				b.WriteString(s[i:])
				return b.String()
			}
			expr := strings.TrimSpace(s[i+2 : i+end])
			if strings.ContainsAny(expr, "(") {
				b.WriteString(s[i : i+end+1])
			} else {
				b.WriteString(textOf(f.get(pointer(base, expr))))
			}
			i += end + 1
		default:
			b.WriteByte(s[i])
			i++
		}
	}
	return b.String()
}

func formatNumber(n float64, decimals int, grouping bool) string {
	s := strconv.FormatFloat(math.Abs(n), 'f', decimals, 64)
	whole, frac, _ := strings.Cut(s, ".")
	if grouping {
		var b strings.Builder
		for i, r := range whole {
			if i > 0 && (len(whole)-i)%3 == 0 {
				b.WriteByte(',')
			}
			b.WriteRune(r)
		}
		whole = b.String()
	}
	if frac != "" {
		whole += "." + frac
	}
	if n < 0 {
		whole = "-" + whole
	}
	return whole
}

func currencyPrefix(code string) string {
	switch strings.ToUpper(code) {
	case "USD":
		return "$"
	case "EUR":
		return "€"
	case "GBP":
		return "£"
	case "JPY":
		return "¥"
	case "":
		return ""
	}
	return strings.ToUpper(code) + " "
}

var dateLayouts = []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02", "15:04:05", "15:04"}

// dateTokens maps Unicode TR35 date pattern tokens to Go layouts.
var dateTokens = map[string]string{
	"yyyy": "2006", "yy": "06", "y": "2006",
	"MMMM": "January", "MMM": "Jan", "MM": "01", "M": "1",
	"dd": "02", "d": "2",
	"EEEE": "Monday", "EEE": "Mon", "EE": "Mon", "E": "Mon",
	"HH": "15", "H": "15", "hh": "03", "h": "3",
	"mm": "04", "m": "4", "ss": "05", "s": "5", "a": "PM",
}

// formatDate formats an ISO 8601 date with a TR35 pattern. A value it cannot
// read is returned as it is.
func formatDate(value, pattern string) string {
	var t time.Time
	parsed := false
	for _, layout := range dateLayouts {
		if v, err := time.Parse(layout, value); err == nil {
			t, parsed = v, true
			break
		}
	}
	if !parsed || pattern == "" {
		return value
	}
	var b strings.Builder
	for i := 0; i < len(pattern); {
		c := pattern[i]
		if c == '\'' {
			end := strings.IndexByte(pattern[i+1:], '\'')
			if end < 0 {
				b.WriteString(pattern[i+1:])
				break
			}
			b.WriteString(pattern[i+1 : i+1+end])
			i += end + 2
			continue
		}
		j := i
		for j < len(pattern) && pattern[j] == c {
			j++
		}
		if layout, ok := dateTokens[pattern[i:j]]; ok {
			b.WriteString(t.Format(layout))
		} else {
			b.WriteString(pattern[i:j])
		}
		i = j
	}
	return b.String()
}

// formButton is a button that answers the task, met while drawing a form.
type formButton struct {
	label   string
	primary bool
	c       map[string]any
	base    string
}

// formWriter draws a form as text. fill, when set, asks for the value of each
// input component as it is drawn; otherwise inputs show their values. Buttons
// that answer the task are gathered in buttons rather than drawn.
type formWriter struct {
	f       *formState
	w       io.Writer
	style   Style
	fill    func(c map[string]any, base, indent string) error
	buttons []formButton
	depth   int
}

func (fw *formWriter) line(indent, text string) {
	for _, l := range strings.Split(text, "\n") {
		fmt.Fprintln(fw.w, indent+l)
	}
}

func (fw *formWriter) draw(id, base, indent string) error {
	c := fw.f.byID[id]
	// A cycle of child IDs would draw forever.
	if c == nil || fw.depth > 64 {
		return nil
	}
	fw.depth++
	defer func() { fw.depth-- }()
	f, style := fw.f, fw.style
	switch c["component"] {
	case "Text":
		text := f.text(c["text"], base)
		switch c["variant"] {
		case "h1", "h2", "h3", "h4", "h5":
			text = style.Bold(text)
		case "caption":
			text = style.Dim(text)
		}
		if text != "" {
			fw.line(indent, text)
		}
	case "Image", "Video", "AudioPlayer":
		kind := map[any]string{"Image": "Image", "Video": "Video", "AudioPlayer": "Audio"}[c["component"]]
		parts := []string{style.Dim(kind + ":")}
		if d := f.text(c["description"], base); d != "" {
			parts = append(parts, d)
		}
		parts = append(parts, f.text(c["url"], base))
		fw.line(indent, strings.Join(parts, " "))
	case "Divider":
		if c["axis"] != "vertical" {
			fw.line(indent, style.Dim(strings.Repeat("─", 40)))
		}
	case "Row":
		if texts, ok := fw.inlineTexts(c, base); ok {
			if len(texts) > 0 {
				fw.line(indent, strings.Join(texts, "  "))
			}
			return nil
		}
		return fw.children(c["children"], base, indent)
	case "Column", "List":
		return fw.children(c["children"], base, indent)
	case "Card":
		child, _ := c["child"].(string)
		return fw.draw(child, base, indent+"  ")
	case "Tabs":
		tabs, _ := c["tabs"].([]any)
		for _, tab := range tabs {
			tab, _ := tab.(map[string]any)
			fw.line(indent, style.Bold(f.text(tab["title"], base)))
			child, _ := tab["child"].(string)
			if err := fw.draw(child, base, indent+"  "); err != nil {
				return err
			}
		}
	case "Modal":
		content, _ := c["content"].(string)
		return fw.draw(content, base, indent)
	case "Button":
		if f.modalTriggers[id] {
			return nil
		}
		label := fw.buttonLabel(c, base)
		if _, ok := eventOf(c); ok {
			fw.buttons = append(fw.buttons, formButton{label: label, primary: c["variant"] == "primary", c: c, base: base})
			return nil
		}
		action, _ := c["action"].(map[string]any)
		call, _ := action["functionCall"].(map[string]any)
		if call["call"] == "openUrl" {
			args, _ := call["args"].(map[string]any)
			fw.line(indent, label+": "+style.Cyan(f.text(args["url"], base)))
		}
	case "TextField", "CheckBox", "ChoicePicker", "Slider", "DateTimeInput":
		if fw.fill != nil {
			return fw.fill(c, base, indent)
		}
		fw.drawInput(c, base, indent)
	}
	return nil
}

// children draws a child list: component IDs, or a template drawn once for
// each item of a list in the data model.
func (fw *formWriter) children(list any, base, indent string) error {
	switch list := list.(type) {
	case []any:
		for _, id := range list {
			id, _ := id.(string)
			if err := fw.draw(id, base, indent); err != nil {
				return err
			}
		}
	case map[string]any:
		id, _ := list["componentId"].(string)
		p, _ := list["path"].(string)
		p = pointer(base, p)
		switch items := fw.f.get(p).(type) {
		case []any:
			for i := range items {
				if err := fw.draw(id, p+"/"+strconv.Itoa(i), indent); err != nil {
					return err
				}
			}
		case map[string]any:
			keys := make([]string, 0, len(items))
			for k := range items {
				keys = append(keys, k)
			}
			slices.Sort(keys)
			for _, k := range keys {
				if err := fw.draw(id, p+"/"+k, indent); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// inlineTexts returns the texts of a row whose children are all text or
// icons, which fit on one line.
func (fw *formWriter) inlineTexts(row map[string]any, base string) ([]string, bool) {
	ids, ok := row["children"].([]any)
	if !ok {
		return nil, false
	}
	var texts []string
	for _, id := range ids {
		id, _ := id.(string)
		c := fw.f.byID[id]
		switch c["component"] {
		case "Text":
			if t := fw.f.text(c["text"], base); t != "" {
				texts = append(texts, t)
			}
		case "Icon":
		default:
			return nil, false
		}
	}
	return texts, true
}

func (fw *formWriter) buttonLabel(c map[string]any, base string) string {
	child := fw.f.byID[fmt.Sprint(c["child"])]
	switch child["component"] {
	case "Text":
		if t := fw.f.text(child["text"], base); t != "" {
			return t
		}
	case "Icon":
		if t := fw.f.text(child["name"], base); t != "" {
			return t
		}
	}
	if a, ok := c["accessibility"].(map[string]any); ok {
		if t := fw.f.text(a["label"], base); t != "" {
			return t
		}
	}
	return fmt.Sprint(c["id"])
}

// selected is a choice picker's selection as a list of option values.
func selected(v any) []string {
	switch v := v.(type) {
	case []any:
		out := make([]string, 0, len(v))
		for _, e := range v {
			out = append(out, textOf(e))
		}
		return out
	case string:
		if v != "" {
			return []string{v}
		}
	}
	return nil
}

type choiceOption struct{ label, value string }

func (fw *formWriter) options(c map[string]any, base string) []choiceOption {
	list, _ := c["options"].([]any)
	out := make([]choiceOption, 0, len(list))
	for _, o := range list {
		o, _ := o.(map[string]any)
		out = append(out, choiceOption{label: fw.f.text(o["label"], base), value: textOf(o["value"])})
	}
	return out
}

func (fw *formWriter) drawInput(c map[string]any, base, indent string) {
	f, style := fw.f, fw.style
	label := f.text(c["label"], base)
	value := f.value(c["value"], base)
	switch c["component"] {
	case "CheckBox":
		box := "[ ]"
		if value == true {
			box = "[x]"
		}
		fw.line(indent, box+" "+label)
	case "ChoicePicker":
		if label != "" {
			fw.line(indent, label)
		}
		chosen := selected(value)
		on, off := "(•)", "( )"
		if c["variant"] == "multipleSelection" {
			on, off = "[x]", "[ ]"
		}
		for _, o := range fw.options(c, base) {
			mark := off
			if slices.Contains(chosen, o.value) {
				mark = on
			}
			fw.line(indent+"  ", mark+" "+o.label)
		}
	default:
		text := textOf(value)
		if c["variant"] == "obscured" && text != "" {
			text = "••••••"
		}
		if c["component"] == "Slider" {
			min, _ := numberOf(c["min"])
			max, _ := numberOf(c["max"])
			text += style.Dim(fmt.Sprintf("  (%s to %s)", textOf(min), textOf(max)))
		}
		if text == "" {
			text = style.Dim("(empty)")
		}
		fw.line(indent, label+": "+text)
	}
}

// failedChecks returns the messages of a component's checks that fail.
func (f *formState) failedChecks(c map[string]any, base string) []string {
	checks, _ := c["checks"].([]any)
	var out []string
	for _, check := range checks {
		check, _ := check.(map[string]any)
		if f.value(check["condition"], base) != true {
			out = append(out, textOf(check["message"]))
		}
	}
	return out
}

// answer is a button's event context, with its bindings read from the data
// model: the task's output.
func (f *formState) answer(b formButton) (json.RawMessage, error) {
	event, _ := eventOf(b.c)
	context, _ := event["context"].(map[string]any)
	out := make(map[string]any, len(context))
	for k, v := range context {
		out[k] = f.value(v, b.base)
	}
	return json.Marshal(out)
}

// drawForm writes a form's surface with data as its data model, and returns
// the buttons that answer the task.
func drawForm(w io.Writer, style Style, s *formSurface, data json.RawMessage, indent string) []formButton {
	fw := &formWriter{f: newForm(s, data), w: w, style: style}
	_ = fw.draw("root", "", indent)
	return fw.buttons
}

// promptForm asks a person at a terminal to fill in a task's a2ui form and
// press one of its buttons, and returns the button's event context as the
// task's output.
func promptForm(ctx context.Context, lr *LineReader, w io.Writer, style Style, s *formSurface, input json.RawMessage) (json.RawMessage, error) {
	f := newForm(s, input)
	for {
		fw := &formWriter{f: f, w: w, style: style}
		fw.fill = func(c map[string]any, base, indent string) error {
			return askInput(ctx, lr, fw, c, base, indent)
		}
		if err := fw.draw("root", "", ""); err != nil {
			return nil, err
		}
		b, err := chooseButton(ctx, lr, w, style, fw.buttons)
		if err != nil {
			return nil, err
		}
		failed := f.failedChecks(b.c, b.base)
		if len(failed) == 0 {
			return f.answer(b)
		}
		for _, msg := range failed {
			fmt.Fprintln(w, style.Red(msg))
		}
		fmt.Fprintln(w, style.Dim("Fill in the form again. Press Enter to keep a value."))
	}
}

func chooseButton(ctx context.Context, lr *LineReader, w io.Writer, style Style, buttons []formButton) (formButton, error) {
	if len(buttons) == 1 {
		_, err := ask(ctx, lr, w, style, fmt.Sprintf("Press Enter to %s ", buttons[0].label))
		return buttons[0], err
	}
	primary := -1
	for i, b := range buttons {
		mark := ""
		if b.primary && primary < 0 {
			primary = i
			mark = style.Dim("  (Enter)")
		}
		fmt.Fprintf(w, "  %d) %s%s\n", i+1, b.label, mark)
	}
	for {
		line, err := ask(ctx, lr, w, style, "number > ")
		if err != nil {
			return formButton{}, err
		}
		line = strings.TrimSpace(line)
		if line == "" && primary >= 0 {
			return buttons[primary], nil
		}
		if n, err := strconv.Atoi(line); err == nil && n >= 1 && n <= len(buttons) {
			return buttons[n-1], nil
		}
		for _, b := range buttons {
			if strings.EqualFold(line, b.label) {
				return b, nil
			}
		}
		fmt.Fprintf(w, "%s\n", style.Red(fmt.Sprintf("Pick a number from 1 to %d.", len(buttons))))
	}
}

// askInput asks for an input component's value and writes it to the data
// model. An input that is not bound to the data model cannot change, so it is
// only drawn.
func askInput(ctx context.Context, lr *LineReader, fw *formWriter, c map[string]any, base, indent string) error {
	f, w, style := fw.f, fw.w, fw.style
	binding, _ := c["value"].(map[string]any)
	path, bound := binding["path"].(string)
	if !bound {
		fw.drawInput(c, base, indent)
		return nil
	}
	p := pointer(base, path)
	label := f.text(c["label"], base)
	kind := c["component"]
	options := fw.options(c, base)
	multiple := c["variant"] == "multipleSelection"

	for {
		cur := f.get(p)
		hint := ""
		switch kind {
		case "CheckBox":
			hint = "[y/n]"
			if cur == true {
				hint = "[Y/n]"
			} else if cur == false {
				hint = "[y/N]"
			}
		case "ChoicePicker":
			if multiple {
				hint = "pick numbers, separated by commas"
			} else {
				hint = "pick a number"
			}
		case "Slider":
			min, _ := numberOf(c["min"])
			max, _ := numberOf(c["max"])
			hint = fmt.Sprintf("%s to %s", textOf(min), textOf(max))
		case "DateTimeInput":
			hint = dateHint(c)
		case "TextField":
			switch c["variant"] {
			case "number":
				hint = "a number"
			case "obscured":
				hint = "shown as you type"
			}
		}
		if hint != "" && kind != "CheckBox" {
			hint = "(" + hint + ")"
		}
		if t := textOf(cur); t != "" && kind != "CheckBox" {
			if c["variant"] == "obscured" {
				t = "••••••"
			}
			if kind == "ChoicePicker" {
				t = strings.Join(selected(cur), ", ")
			}
			hint = strings.TrimSpace(hint + " [" + t + "]")
		}
		head := style.Bold(label)
		if hint != "" {
			head += " " + style.Dim(hint)
		}
		fw.line(indent, head)
		if kind == "ChoicePicker" {
			for i, o := range options {
				fw.line(indent, fmt.Sprintf("  %d) %s", i+1, o.label))
			}
		}
		line, err := ask(ctx, lr, w, style, indent+"> ")
		if err != nil {
			return err
		}
		line = strings.TrimSpace(line)
		if line != "" {
			v, err := inputValue(c, line, options, multiple)
			if err != nil {
				fmt.Fprintln(w, indent+style.Red(err.Error()))
				continue
			}
			f.set(p, v)
		}
		if failed := f.failedChecks(c, base); len(failed) > 0 {
			for _, msg := range failed {
				fmt.Fprintln(w, indent+style.Red(msg))
			}
			continue
		}
		return nil
	}
}

// inputValue reads what was typed for an input component, as the kind of
// value the component writes.
func inputValue(c map[string]any, line string, options []choiceOption, multiple bool) (any, error) {
	switch c["component"] {
	case "CheckBox":
		switch strings.ToLower(line) {
		case "y", "yes", "true":
			return true, nil
		case "n", "no", "false":
			return false, nil
		}
		return nil, fmt.Errorf("answer yes or no")
	case "ChoicePicker":
		var out []any
		for _, item := range strings.Split(line, ",") {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			i := slices.IndexFunc(options, func(o choiceOption) bool {
				return strings.EqualFold(item, o.value) || strings.EqualFold(item, o.label)
			})
			if n, err := strconv.Atoi(item); err == nil {
				if n < 1 || n > len(options) {
					return nil, fmt.Errorf("pick a number from 1 to %d", len(options))
				}
				i = n - 1
			}
			if i < 0 {
				return nil, fmt.Errorf("%q is not one of the choices", item)
			}
			out = append(out, options[i].value)
		}
		if !multiple && len(out) > 1 {
			return nil, fmt.Errorf("pick one")
		}
		return out, nil
	case "Slider":
		n, err := strconv.ParseFloat(line, 64)
		if err != nil {
			return nil, fmt.Errorf("%q is not a number", line)
		}
		min, _ := numberOf(c["min"])
		if max, ok := numberOf(c["max"]); ok && (n < min || n > max) {
			return nil, fmt.Errorf("pick a number from %s to %s", textOf(min), textOf(max))
		}
		return n, nil
	case "DateTimeInput":
		for _, layout := range dateInputLayouts(c) {
			if _, err := time.Parse(layout, line); err == nil {
				return line, nil
			}
		}
		return nil, fmt.Errorf("type it as %s", dateHint(c))
	}
	if c["variant"] == "number" {
		if _, err := strconv.ParseFloat(line, 64); err != nil {
			return nil, fmt.Errorf("%q is not a number", line)
		}
	}
	if pattern, ok := c["validationRegexp"].(string); ok {
		if re, err := regexp.Compile(pattern); err == nil && !re.MatchString(line) {
			return nil, fmt.Errorf("%q is not in the expected format", line)
		}
	}
	return line, nil
}

func dateInputLayouts(c map[string]any) []string {
	date, clock := c["enableDate"] == true, c["enableTime"] == true
	switch {
	case date && clock:
		return []string{"2006-01-02T15:04", "2006-01-02T15:04:05", time.RFC3339}
	case clock:
		return []string{"15:04", "15:04:05"}
	}
	return []string{"2006-01-02"}
}

func dateHint(c map[string]any) string {
	date, clock := c["enableDate"] == true, c["enableTime"] == true
	switch {
	case date && clock:
		return "YYYY-MM-DDTHH:MM"
	case clock:
		return "HH:MM"
	}
	return "YYYY-MM-DD"
}
