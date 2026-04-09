package ui

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/codysnider/tagmem/internal/importer"
	"github.com/codysnider/tagmem/internal/store"
	"github.com/codysnider/tagmem/internal/taggraph"
	"github.com/codysnider/tagmem/internal/vector"
	"github.com/codysnider/tagmem/internal/xdg"
)

type focusArea int

const (
	focusSearch focusArea = iota
	focusDepths
	focusTags
	focusEntries
)

type screenMode int

const (
	modeNormal screenMode = iota
	modeModal
	modeConfirmDelete
	modeForm
)

type formKind int

const (
	formAdd formKind = iota
	formEdit
	formClipboard
	formImport
)

type depthItem struct {
	depth int
	count int
	all   bool
}

func (i depthItem) FilterValue() string { return i.Title() }
func (i depthItem) Title() string {
	if i.all {
		return "All depths"
	}
	return fmt.Sprintf("Depth %d", i.depth)
}
func (i depthItem) Description() string {
	if i.count == 1 {
		return "1 entry"
	}
	return fmt.Sprintf("%d entries", i.count)
}

type entryItem struct{ entry store.Entry }

func (i entryItem) FilterValue() string {
	return strings.Join([]string{i.entry.Title, i.entry.Body, strings.Join(i.entry.Tags, " "), i.entry.Source}, " ")
}
func (i entryItem) Title() string { return i.entry.Title }
func (i entryItem) Description() string {
	parts := []string{fmt.Sprintf("depth %d", i.entry.Depth), i.entry.UpdatedAt.Format("2006-01-02 15:04")}
	if len(i.entry.Tags) > 0 {
		parts = append(parts, strings.Join(i.entry.Tags, ", "))
	}
	return strings.Join(parts, "  ")
}

type tagItem struct {
	name  string
	count int
	all   bool
}

func (i tagItem) FilterValue() string { return i.Title() }
func (i tagItem) Title() string {
	if i.all {
		return "All tags"
	}
	return i.name
}
func (i tagItem) Description() string {
	if i.count == 1 {
		return "1 entry"
	}
	return fmt.Sprintf("%d entries", i.count)
}

type simpleDelegate struct{}

func (d simpleDelegate) Height() int                             { return 2 }
func (d simpleDelegate) Spacing() int                            { return 0 }
func (d simpleDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }
func (d simpleDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	renderable, ok := item.(interface {
		Title() string
		Description() string
	})
	if !ok {
		return
	}
	titleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	descriptionStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	if index == m.Index() {
		titleStyle = titleStyle.Bold(true).Foreground(lipgloss.Color("86"))
		descriptionStyle = descriptionStyle.Foreground(lipgloss.Color("249"))
	}
	_, _ = fmt.Fprint(w, titleStyle.Render(renderable.Title()))
	_, _ = fmt.Fprint(w, "\n")
	_, _ = fmt.Fprint(w, descriptionStyle.Render(renderable.Description()))
}

type formModel struct {
	kind       formKind
	entryID    int
	focusIndex int
	title      textinput.Model
	depth      textinput.Model
	tags       textinput.Model
	source     textinput.Model
	body       textarea.Model
	path       textinput.Model
	mode       textinput.Model
	extract    textinput.Model
	help       string
	titleText  string
}

type model struct {
	repo            *store.Repository
	paths           xdg.Paths
	provider        vector.Provider
	search          textinput.Model
	depths          list.Model
	tags            list.Model
	entries         list.Model
	focus           focusArea
	mode            screenMode
	width           int
	height          int
	lastError       error
	status          string
	modalTitle      string
	modalBody       string
	confirmDeleteID int
	form            formModel
}

func Run(repo *store.Repository, paths xdg.Paths, provider vector.Provider) error {
	program := tea.NewProgram(newModel(repo, paths, provider), tea.WithAltScreen())
	_, err := program.Run()
	return err
}

func newModel(repo *store.Repository, paths xdg.Paths, provider vector.Provider) model {
	search := textinput.New()
	search.Placeholder = "Search entries"
	search.Prompt = "Search: "
	search.Focus()

	depths := list.New([]list.Item{}, simpleDelegate{}, 20, 10)
	configureList(&depths)
	tags := list.New([]list.Item{}, simpleDelegate{}, 20, 10)
	configureList(&tags)
	entries := list.New([]list.Item{}, simpleDelegate{}, 40, 10)
	configureList(&entries)

	m := model{repo: repo, paths: paths, provider: provider, search: search, depths: depths, tags: tags, entries: entries, focus: focusSearch, mode: modeNormal, status: paths.StorePath}
	if err := m.refreshData(); err != nil {
		m.lastError = err
	}
	return m
}

func configureList(m *list.Model) {
	m.SetShowTitle(false)
	m.SetShowStatusBar(false)
	m.SetShowHelp(false)
	m.SetFilteringEnabled(false)
	m.DisableQuitKeybindings()
}

func (m model) Init() tea.Cmd { return textinput.Blink }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resize()
		return m, nil
	case tea.KeyMsg:
		switch m.mode {
		case modeModal:
			if msg.String() == "esc" || msg.String() == "enter" || msg.String() == "q" {
				m.mode = modeNormal
				return m, nil
			}
			return m, nil
		case modeConfirmDelete:
			switch strings.ToLower(msg.String()) {
			case "y":
				if _, err := m.repo.Delete(m.confirmDeleteID); err != nil {
					m.lastError = err
				} else {
					_ = m.refreshData()
				}
				m.mode = modeNormal
				return m, nil
			case "n", "esc":
				m.mode = modeNormal
				return m, nil
			}
		case modeForm:
			return m.updateForm(msg)
		default:
			return m.updateNormal(msg)
		}
	}
	return m, nil
}

func (m model) updateNormal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "left":
		m.focus = (m.focus + 3) % 4
		m.applyFocus()
		return m, nil
	case "right":
		m.focus = (m.focus + 1) % 4
		m.applyFocus()
		return m, nil
	case "h":
		m.openHelpModal()
		return m, nil
	case "/":
		m.focus = focusSearch
		m.applyFocus()
		return m, nil
	case "r":
		m.refreshWithStatus("Reloaded from disk")
		return m, nil
	case "s":
		m.openStatusModal()
		return m, nil
	case "d":
		m.openDoctorModal()
		return m, nil
	case "a":
		m.openEntryForm(formAdd, store.Entry{}, "Add entry")
		return m, nil
	case "e":
		if entry, ok := m.selectedEntry(); ok {
			m.openEntryForm(formEdit, entry, "Edit entry")
		}
		return m, nil
	case "x":
		if entry, ok := m.selectedEntry(); ok {
			m.confirmDeleteID = entry.ID
			m.mode = modeConfirmDelete
		}
		return m, nil
	case "c":
		m.openClipboardForm()
		return m, nil
	case "i":
		m.openImportForm()
		return m, nil
	}

	var cmd tea.Cmd
	switch m.focus {
	case focusSearch:
		before := m.search.Value()
		m.search, cmd = m.search.Update(msg)
		if m.search.Value() != before {
			m.refreshEntriesOnly()
		}
	case focusDepths:
		before := m.selectedDepthKey()
		m.depths, cmd = m.depths.Update(msg)
		if m.selectedDepthKey() != before {
			m.refreshEntriesOnly()
		}
	case focusTags:
		before := m.selectedTagKey()
		m.tags, cmd = m.tags.Update(msg)
		if m.selectedTagKey() != before {
			m.refreshEntriesOnly()
		}
	case focusEntries:
		m.entries, cmd = m.entries.Update(msg)
		m.syncStatus()
	}
	return m, cmd
}

func (m model) updateForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeNormal
		return m, nil
	case "tab", "down":
		m.form.focusIndex = (m.form.focusIndex + 1) % m.formFieldCount()
		m.applyFormFocus()
		return m, nil
	case "shift+tab", "up":
		m.form.focusIndex = (m.form.focusIndex + m.formFieldCount() - 1) % m.formFieldCount()
		m.applyFormFocus()
		return m, nil
	case "ctrl+s":
		m.submitForm()
		return m, nil
	}
	var cmd tea.Cmd
	switch m.form.kind {
	case formImport:
		cmd = m.updateImportFormField(msg)
	default:
		cmd = m.updateEntryFormField(msg)
	}
	return m, cmd
}

func (m *model) updateEntryFormField(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	switch m.form.focusIndex {
	case 0:
		m.form.title, cmd = m.form.title.Update(msg)
	case 1:
		m.form.depth, cmd = m.form.depth.Update(msg)
	case 2:
		m.form.tags, cmd = m.form.tags.Update(msg)
	case 3:
		m.form.source, cmd = m.form.source.Update(msg)
	case 4:
		m.form.body, cmd = m.form.body.Update(msg)
	}
	return cmd
}

func (m *model) updateImportFormField(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	switch m.form.focusIndex {
	case 0:
		m.form.path, cmd = m.form.path.Update(msg)
	case 1:
		m.form.mode, cmd = m.form.mode.Update(msg)
	case 2:
		m.form.extract, cmd = m.form.extract.Update(msg)
	case 3:
		m.form.depth, cmd = m.form.depth.Update(msg)
	}
	return cmd
}

func (m model) View() string {
	if m.width == 0 || m.height == 0 {
		return "loading..."
	}
	header := lipgloss.NewStyle().Bold(true).Render("tagmem")
	status := lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render(m.statusLine())
	searchPane := m.renderPane("Search", m.search.View(), m.mode == modeNormal && m.focus == focusSearch, m.width-2, 3)
	mainHeight := max(8, m.height-8)
	depthsWidth := max(22, m.width/5)
	tagsWidth := max(22, m.width/5)
	entriesWidth := max(30, m.width/4)
	detailWidth := max(30, m.width-depthsWidth-tagsWidth-entriesWidth-8)
	depthsPane := m.renderPane("Depths", m.depths.View(), m.mode == modeNormal && m.focus == focusDepths, depthsWidth, mainHeight)
	tagsPane := m.renderPane("Tags", m.tags.View(), m.mode == modeNormal && m.focus == focusTags, tagsWidth, mainHeight)
	entriesPane := m.renderPane("Entries", m.entries.View(), m.mode == modeNormal && m.focus == focusEntries, entriesWidth, mainHeight)
	detailPane := m.renderPane("Detail", m.detailText(), false, detailWidth, mainHeight)
	body := lipgloss.JoinHorizontal(lipgloss.Top, tagsPane, depthsPane, entriesPane, detailPane)
	footer := lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render("Arrows move  / search  H help  A add  E edit  X delete  I import  C clipboard  S status  D doctor  R reload  Q quit")
	view := lipgloss.JoinVertical(lipgloss.Left, header, status, searchPane, body, footer)
	switch m.mode {
	case modeModal:
		return overlay(view, m.renderModal(m.modalTitle, m.modalBody), m.width, m.height)
	case modeConfirmDelete:
		return overlay(view, m.renderModal("Delete entry", fmt.Sprintf("Delete entry %d?\n\nY delete  N cancel", m.confirmDeleteID)), m.width, m.height)
	case modeForm:
		return overlay(view, m.renderForm(), m.width, m.height)
	default:
		return view
	}
}

func (m *model) refreshWithStatus(message string) {
	if err := m.refreshData(); err != nil {
		m.lastError = err
		return
	}
	m.lastError = nil
	m.status = message
	m.syncStatus()
}

func (m *model) refreshData() error {
	if err := m.refreshDepths(); err != nil {
		return err
	}
	if err := m.refreshTags(); err != nil {
		return err
	}
	return m.refreshEntries()
}

func (m *model) refreshDepths() error {
	summaries, err := m.repo.DepthCounts()
	if err != nil {
		return err
	}
	selected := m.selectedDepthKey()
	items := make([]list.Item, 0, len(summaries)+1)
	total := 0
	for _, summary := range summaries {
		total += summary.Count
	}
	items = append(items, depthItem{all: true, count: total})
	for _, summary := range summaries {
		items = append(items, depthItem{depth: summary.Depth, count: summary.Count})
	}
	m.depths.SetItems(items)
	m.selectDepthByKey(selected)
	return nil
}

func (m *model) refreshTags() error {
	entries, err := m.repo.List(store.Query{Limit: 0})
	if err != nil {
		return err
	}
	selected := m.selectedTagKey()
	counts := taggraph.TagCounts(entries)
	items := make([]list.Item, 0, len(counts)+1)
	items = append(items, tagItem{all: true, count: len(entries)})
	type pair struct {
		name  string
		count int
	}
	pairs := make([]pair, 0, len(counts))
	for name, count := range counts {
		pairs = append(pairs, pair{name, count})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].count == pairs[j].count {
			return pairs[i].name < pairs[j].name
		}
		return pairs[i].count > pairs[j].count
	})
	for _, pair := range pairs {
		items = append(items, tagItem{name: pair.name, count: pair.count})
	}
	m.tags.SetItems(items)
	m.selectTagByKey(selected)
	return nil
}

func (m *model) refreshEntriesOnly() { _ = m.refreshEntries() }

func (m *model) refreshEntries() error {
	query := store.Query{Text: m.search.Value(), Limit: 200}
	if selected, ok := m.selectedDepth(); ok {
		query.Depth = &selected
	}
	if selected, ok := m.selectedTag(); ok {
		query.Tag = selected
	}
	entries, err := m.repo.Search(query)
	if err != nil {
		return err
	}
	selectedID := m.selectedEntryID()
	items := make([]list.Item, 0, len(entries))
	for _, entry := range entries {
		items = append(items, entryItem{entry: entry})
	}
	m.entries.SetItems(items)
	m.selectEntryByID(selectedID)
	m.syncStatus()
	return nil
}

func (m *model) resize() {
	searchWidth := max(20, m.width-16)
	m.search.Width = searchWidth
	mainHeight := max(6, m.height-12)
	m.depths.SetHeight(mainHeight)
	m.tags.SetHeight(mainHeight)
	m.entries.SetHeight(mainHeight)
	if m.mode == modeForm {
		m.form.body.SetWidth(max(30, m.width/2))
		m.form.body.SetHeight(max(8, m.height/4))
	}
	if items := len(m.depths.Items()); items < mainHeight {
		m.depths.SetHeight(max(4, items*2))
	}
	if items := len(m.tags.Items()); items < mainHeight {
		m.tags.SetHeight(max(4, items*2))
	}
	if items := len(m.entries.Items()); items < mainHeight {
		m.entries.SetHeight(max(4, items*2))
	}
}

func (m *model) applyFocus() {
	if m.focus == focusSearch {
		m.search.Focus()
		return
	}
	m.search.Blur()
}

func (m *model) applyFormFocus() {
	for _, input := range []*textinput.Model{&m.form.title, &m.form.depth, &m.form.tags, &m.form.source, &m.form.path, &m.form.mode, &m.form.extract} {
		input.Blur()
	}
	m.form.body.Blur()
	switch m.form.kind {
	case formImport:
		switch m.form.focusIndex {
		case 0:
			m.form.path.Focus()
		case 1:
			m.form.mode.Focus()
		case 2:
			m.form.extract.Focus()
		case 3:
			m.form.depth.Focus()
		}
	default:
		switch m.form.focusIndex {
		case 0:
			m.form.title.Focus()
		case 1:
			m.form.depth.Focus()
		case 2:
			m.form.tags.Focus()
		case 3:
			m.form.source.Focus()
		case 4:
			m.form.body.Focus()
		}
	}
}

func (m model) renderPane(title, body string, focused bool, width, height int) string {
	borderColor := lipgloss.Color("238")
	if focused {
		borderColor = lipgloss.Color("86")
	}
	style := lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(borderColor).Padding(0, 1).Width(width).Height(height)
	header := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("252")).Render(title)
	return style.Render(lipgloss.JoinVertical(lipgloss.Left, header, body))
}

func (m model) renderModal(title, body string) string {
	style := lipgloss.NewStyle().Border(lipgloss.DoubleBorder()).BorderForeground(lipgloss.Color("86")).Padding(1, 2).Width(max(50, min(m.width-8, 90)))
	return style.Render(lipgloss.JoinVertical(lipgloss.Left, lipgloss.NewStyle().Bold(true).Render(title), body))
}

func (m model) renderForm() string {
	lines := []string{lipgloss.NewStyle().Bold(true).Render(m.form.titleText)}
	if m.form.kind == formImport {
		lines = append(lines,
			fieldLine("Path", m.form.path.View(), m.form.focusIndex == 0),
			fieldLine("Mode", m.form.mode.View(), m.form.focusIndex == 1),
			fieldLine("Extract", m.form.extract.View(), m.form.focusIndex == 2),
			fieldLine("Depth", m.form.depth.View(), m.form.focusIndex == 3),
		)
	} else {
		lines = append(lines,
			fieldLine("Title", m.form.title.View(), m.form.focusIndex == 0),
			fieldLine("Depth", m.form.depth.View(), m.form.focusIndex == 1),
			fieldLine("Tags", m.form.tags.View(), m.form.focusIndex == 2),
			fieldLine("Source", m.form.source.View(), m.form.focusIndex == 3),
			fieldLine("Body", m.form.body.View(), m.form.focusIndex == 4),
		)
	}
	lines = append(lines, "", lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render(m.form.help))
	style := lipgloss.NewStyle().Border(lipgloss.DoubleBorder()).BorderForeground(lipgloss.Color("86")).Padding(1, 2).Width(max(60, min(m.width-6, 100)))
	return style.Render(strings.Join(lines, "\n"))
}

func fieldLine(label, value string, focused bool) string {
	labelStyle := lipgloss.NewStyle().Width(10).Foreground(lipgloss.Color("245"))
	if focused {
		labelStyle = labelStyle.Foreground(lipgloss.Color("86")).Bold(true)
	}
	return labelStyle.Render(label+":") + " " + value
}

func (m model) detailText() string {
	item, ok := m.entries.SelectedItem().(entryItem)
	if !ok {
		return "No entry selected.\n\nUse A to add, I to import, C to import from clipboard."
	}
	lines := []string{fmt.Sprintf("ID: %d", item.entry.ID), fmt.Sprintf("Depth: %d", item.entry.Depth), fmt.Sprintf("Created: %s", item.entry.CreatedAt.Format("2006-01-02 15:04")), fmt.Sprintf("Updated: %s", item.entry.UpdatedAt.Format("2006-01-02 15:04"))}
	if item.entry.Source != "" {
		lines = append(lines, "Source: "+item.entry.Source)
	}
	if len(item.entry.Tags) > 0 {
		lines = append(lines, "Tags: "+strings.Join(item.entry.Tags, ", "))
	}
	lines = append(lines, "", item.entry.Title, "", item.entry.Body)
	return strings.Join(lines, "\n")
}

func (m *model) syncStatus() {
	count := len(m.entries.Items())
	selectedDepth := "all depths"
	if depth, ok := m.selectedDepth(); ok {
		selectedDepth = fmt.Sprintf("depth %d", depth)
	}
	m.status = fmt.Sprintf("%s  |  %d visible entries  |  %s", m.paths.StorePath, count, selectedDepth)
	if m.lastError != nil {
		m.status = fmt.Sprintf("%s  |  error: %v", m.status, m.lastError)
	}
}

func (m model) statusLine() string { return m.status }

func (m *model) openStatusModal() {
	entries, err := m.repo.List(store.Query{Limit: 0})
	if err != nil {
		m.lastError = err
		return
	}
	depths, err := m.repo.DepthCounts()
	if err != nil {
		m.lastError = err
		return
	}
	tags := taggraph.TagCounts(entries)
	lines := []string{fmt.Sprintf("Entries: %d", len(entries)), "", "Depths:"}
	for _, depth := range depths {
		lines = append(lines, fmt.Sprintf("  depth %d: %d", depth.Depth, depth.Count))
	}
	if len(tags) > 0 {
		lines = append(lines, "", "Top tags:")
		type tagCount struct {
			name  string
			count int
		}
		items := make([]tagCount, 0, len(tags))
		for name, count := range tags {
			items = append(items, tagCount{name, count})
		}
		sort.Slice(items, func(i, j int) bool {
			if items[i].count == items[j].count {
				return items[i].name < items[j].name
			}
			return items[i].count > items[j].count
		})
		for i, item := range items {
			if i >= 12 {
				break
			}
			lines = append(lines, fmt.Sprintf("  %s: %d", item.name, item.count))
		}
	}
	m.modalTitle, m.modalBody, m.mode = "Status", strings.Join(lines, "\n"), modeModal
}

func (m *model) openDoctorModal() {
	report := m.provider.Doctor(context.Background())
	lines := []string{fmt.Sprintf("Provider: %s", report.Provider), fmt.Sprintf("Model: %s", report.Model), fmt.Sprintf("Device: %s", report.ExecutionDevice), fmt.Sprintf("Dimensions: %d", report.EmbeddingDimensions), fmt.Sprintf("Embed test: %s", yesNo(report.EmbeddingWorks))}
	if report.RuntimeLibrary != "" {
		lines = append(lines, "Runtime: "+report.RuntimeLibrary)
	}
	if report.Error != "" {
		lines = append(lines, "", "Error: "+report.Error)
	}
	m.modalTitle, m.modalBody, m.mode = "Doctor", strings.Join(lines, "\n"), modeModal
}

func (m *model) openHelpModal() {
	lines := []string{
		"Navigation",
		"  Left / Right   move between panes",
		"  Up / Down      move inside the focused pane",
		"  /              focus search",
		"",
		"Actions",
		"  H  help",
		"  S  status",
		"  D  doctor",
		"  R  reload",
		"  A  add entry",
		"  E  edit selected entry",
		"  X  delete selected entry",
		"  I  import from path",
		"  C  import from clipboard",
		"  Q  quit",
		"",
		"Forms",
		"  Tab / Up / Down   move fields",
		"  Ctrl+S            save",
		"  Esc               cancel",
	}
	m.modalTitle, m.modalBody, m.mode = "Help", strings.Join(lines, "\n"), modeModal
}

func (m *model) openEntryForm(kind formKind, entry store.Entry, title string) {
	m.mode = modeForm
	m.form = newEntryForm(kind, entry, title)
	m.applyFormFocus()
}

func newEntryForm(kind formKind, entry store.Entry, title string) formModel {
	titleInput := textinput.New()
	titleInput.SetValue(entry.Title)
	depthInput := textinput.New()
	depthInput.SetValue(strconv.Itoa(max(entry.Depth, 1)))
	tagsInput := textinput.New()
	tagsInput.SetValue(strings.Join(entry.Tags, ","))
	sourceInput := textinput.New()
	sourceInput.SetValue(entry.Source)
	bodyInput := textarea.New()
	bodyInput.SetValue(entry.Body)
	bodyInput.ShowLineNumbers = false
	bodyInput.SetHeight(8)
	bodyInput.SetWidth(60)
	return formModel{kind: kind, entryID: entry.ID, focusIndex: 0, title: titleInput, depth: depthInput, tags: tagsInput, source: sourceInput, body: bodyInput, titleText: title, help: "Tab/Up/Down move fields  Ctrl+S save  Esc cancel"}
}

func (m *model) openClipboardForm() {
	text, err := clipboard.ReadAll()
	if err != nil || strings.TrimSpace(text) == "" {
		m.lastError = fmt.Errorf("clipboard is empty or unavailable")
		return
	}
	entry := store.Entry{Depth: 1, Title: "Clipboard import", Body: text, Source: "clipboard"}
	m.openEntryForm(formClipboard, entry, "Import from clipboard")
}

func (m *model) openImportForm() {
	pathInput := textinput.New()
	pathInput.Placeholder = "/path/to/dir/or/file"
	modeInput := textinput.New()
	modeInput.SetValue("files")
	extractInput := textinput.New()
	extractInput.SetValue("exchange")
	depthInput := textinput.New()
	depthInput.SetValue("1")
	m.mode = modeForm
	m.form = formModel{kind: formImport, focusIndex: 0, path: pathInput, mode: modeInput, extract: extractInput, depth: depthInput, titleText: "Import content", help: "Mode: files or conversations  Extract: exchange or general  Ctrl+S import  Esc cancel"}
	m.applyFormFocus()
}

func (m *model) submitForm() {
	if m.form.kind == formImport {
		m.submitImportForm()
		return
	}
	depth, err := strconv.Atoi(strings.TrimSpace(m.form.depth.Value()))
	if err != nil {
		m.lastError = fmt.Errorf("depth must be a number")
		return
	}
	req := store.AddEntry{Depth: depth, Title: m.form.title.Value(), Body: m.form.body.Value(), Tags: parseCSV(m.form.tags.Value()), Source: m.form.source.Value()}
	switch m.form.kind {
	case formAdd, formClipboard:
		if _, err := m.repo.Add(req); err != nil {
			m.lastError = err
			return
		}
	case formEdit:
		if _, ok, err := m.repo.Update(m.form.entryID, req); err != nil {
			m.lastError = err
			return
		} else if !ok {
			m.lastError = fmt.Errorf("entry not found")
			return
		}
	}
	m.mode = modeNormal
	m.refreshWithStatus("Saved entry")
}

func (m *model) submitImportForm() {
	depth, err := strconv.Atoi(strings.TrimSpace(m.form.depth.Value()))
	if err != nil {
		m.lastError = fmt.Errorf("depth must be a number")
		return
	}
	modeValue := importer.Mode(strings.TrimSpace(m.form.mode.Value()))
	if modeValue != importer.ModeFiles && modeValue != importer.ModeConversations {
		m.lastError = fmt.Errorf("mode must be files or conversations")
		return
	}
	result, err := importer.Run(m.repo, importer.Options{SourceDir: strings.TrimSpace(m.form.path.Value()), Mode: modeValue, Extract: strings.TrimSpace(m.form.extract.Value()), Depth: depth, SkipExisting: true, RespectGitignore: true, Provider: &m.provider})
	if err != nil {
		m.lastError = err
		return
	}
	m.mode = modeNormal
	m.refreshWithStatus(fmt.Sprintf("Imported %d entries from %d files", result.EntriesAdded, result.FilesProcessed))
}

func parseCSV(input string) []string {
	if strings.TrimSpace(input) == "" {
		return nil
	}
	parts := strings.Split(input, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func (m model) formFieldCount() int {
	if m.form.kind == formImport {
		return 4
	}
	return 5
}

func (m model) selectedDepth() (int, bool) {
	item, ok := m.depths.SelectedItem().(depthItem)
	if !ok || item.all {
		return 0, false
	}
	return item.depth, true
}
func (m model) selectedDepthKey() string {
	item, ok := m.depths.SelectedItem().(depthItem)
	if !ok || item.all {
		return "all"
	}
	return fmt.Sprintf("depth:%d", item.depth)
}
func (m *model) selectDepthByKey(key string) {
	items := m.depths.Items()
	for i, raw := range items {
		item, ok := raw.(depthItem)
		if !ok {
			continue
		}
		if key == "all" && item.all {
			m.depths.Select(i)
			return
		}
		if !item.all && key == fmt.Sprintf("depth:%d", item.depth) {
			m.depths.Select(i)
			return
		}
	}
	if len(items) > 0 {
		m.depths.Select(0)
	}
}
func (m model) selectedTag() (string, bool) {
	item, ok := m.tags.SelectedItem().(tagItem)
	if !ok || item.all {
		return "", false
	}
	return item.name, true
}
func (m model) selectedTagKey() string {
	item, ok := m.tags.SelectedItem().(tagItem)
	if !ok || item.all {
		return "all"
	}
	return item.name
}
func (m *model) selectTagByKey(key string) {
	items := m.tags.Items()
	for i, raw := range items {
		item, ok := raw.(tagItem)
		if !ok {
			continue
		}
		if key == "all" && item.all {
			m.tags.Select(i)
			return
		}
		if !item.all && key == item.name {
			m.tags.Select(i)
			return
		}
	}
	if len(items) > 0 {
		m.tags.Select(0)
	}
}
func (m model) selectedEntryID() int {
	item, ok := m.entries.SelectedItem().(entryItem)
	if !ok {
		return 0
	}
	return item.entry.ID
}
func (m model) selectedEntry() (store.Entry, bool) {
	item, ok := m.entries.SelectedItem().(entryItem)
	if !ok {
		return store.Entry{}, false
	}
	return item.entry, true
}
func (m *model) selectEntryByID(id int) {
	items := m.entries.Items()
	for i, raw := range items {
		item, ok := raw.(entryItem)
		if !ok {
			continue
		}
		if item.entry.ID == id {
			m.entries.Select(i)
			return
		}
	}
	if len(items) > 0 {
		m.entries.Select(0)
	}
}

func overlay(base, modal string, width, height int) string {
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, modal, lipgloss.WithWhitespaceChars(" "), lipgloss.WithWhitespaceForeground(lipgloss.Color("0")))
}

func yesNo(ok bool) string {
	if ok {
		return "yes"
	}
	return "no"
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
