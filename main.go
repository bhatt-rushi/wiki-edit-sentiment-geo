package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"math/rand"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/bregydoc/gtranslate"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	_ "github.com/mattn/go-sqlite3" // SQLite driver
	"github.com/muesli/reflow/wordwrap"
)

const DB_PATH = "data/wiki.db"

type tickMsg time.Time

// App States
type appState int

const (
	stateReview appState = iota
	stateSettings
	stateDashboard
)

// Sorting Options
const (
	SortBiasDesc      = "Bias Score (High -> Low)"
	SortBiasAsc       = "Bias Score (Low -> High)"
	SortDiffDesc      = "Bias Delta (High -> Low)" // Most increase in bias
	SortDiffAsc       = "Bias Delta (Low -> High)" // Most decrease in bias
	SortTimeNewest    = "Time (Newest First)"
	SortTimeOldest    = "Time (Oldest First)"
	SortRandom        = "Random"
)

var sortOptions = []string{
	SortBiasDesc,
	SortBiasAsc,
	SortDiffDesc,
	SortDiffAsc,
	SortTimeNewest,
	SortTimeOldest,
	SortRandom,
}

// Styles
var (
	docStyle      = lipgloss.NewStyle().Margin(1, 2)
	titleStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("63")).Bold(true)
	infoStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	helpStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	diffBoxStyle  = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("63")).Padding(1, 2)
	selectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Bold(true)
	birdStyle     = lipgloss.NewStyle().PaddingLeft(2)
	successStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
)

// Bird frames
var (
	birdStanding = `>('.')<`
	birdJumping  = `>('o')<`
)

// Revision represents a single revision entry in the database
type Revision struct {
	RevisionID        string
	OriginalRevid     int
	ArticleURL        string
	User              string
	Timestamp         string
	DiffBefore        string
	DiffAfter         string
	ChangeType        string
	ChangeDesc        string
	BiasScoreBefore   float64
	BiasScoreAfter    float64
	BiasDelta         float64
	BiasLabelBefore   string
	BiasLabelAfter    string
	Topic             string
	AIPoliticalStance string
	IsIP              bool
}

// Model represents the Bubble Tea model
type model struct {
	db                *sql.DB
	unscoredRevisions []Revision

	// Categorization Data
	biasCategories  []string
	topicCategories []string

	// State
	state           appState
	currentRevision Revision
	currentStep     int // 0: Select Bias, 1: Select Topic

	dashboard DashboardModel

	selectedBias  string
	selectedTopic string

	// Settings / Filtering
	filterDesc     string
	filterTopic    string
	filterStance   string
	currentSort    string
	shouldClearDB  bool // Flag to trigger DB clear on form submit
	settingsCursor int

	// Feedback
	statusMessage string
	statusTimer   int // Ticks to show status

	// UI State
	uniqueDescs   []string // Cache of unique descriptions
	uniqueTopics  []string // Cache of unique topics from DB
	uniqueStances []string // Cache of unique political stances from DB
	choices       []string // Current choices to display (points to biasCategories or topicCategories)
	cursor        int      // which item our cursor is pointing at
	quitting      bool
	width         int
	height        int
	birdFrame     string
	scoredCount   int

	// Caching and Pre-loading
	diffCache map[string]string
	isReady   bool

	viewport viewport.Model
}

type diffProcessedMsg struct {
	id      string
	content string
}

func newModel(db *sql.DB, biasCats []string, topicCats []string, scoredCount int) model {
	vp := viewport.New(0, 0)
	vp.Style = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("63")).
		PaddingRight(2)

	m := model{
		db:              db,
		biasCategories:  biasCats,
		topicCategories: topicCats,
		choices:         biasCats, // Start with Bias categories
		currentStep:     0,        // Start at step 0
		birdFrame:       birdStanding,
		scoredCount:     scoredCount,
		diffCache:       make(map[string]string),
		isReady:         false,
		viewport:        vp,
		state:           stateReview,
		currentSort:     SortBiasDesc, // Default sort
	}

	m.uniqueDescs = m.getUniqueDescriptions()
	m.uniqueTopics = m.getUniqueTopics()
	m.uniqueStances = m.getUniqueStances()

	// Initial fetch with default settings
	m.fetchRevisions()
	return m
}

func (m *model) getUniqueDescriptions() []string {
	rows, err := m.db.Query("SELECT change_desc, COUNT(*) as cnt FROM revisions WHERE manual_bias IS NULL GROUP BY change_desc ORDER BY cnt DESC LIMIT 50")
	if err != nil {
		logToFile(fmt.Sprintf("Failed to fetch descriptions: %v", err))
		return []string{}
	}
	defer rows.Close()

	var descs []string
	for rows.Next() {
		var desc string
		var count int
		if err := rows.Scan(&desc, &count); err == nil {
			if desc != "" {
				descs = append(descs, desc)
			}
		}
	}
	return descs
}

func (m *model) getUniqueTopics() []string {
	rows, err := m.db.Query("SELECT ai_topic, COUNT(*) as cnt FROM revisions WHERE manual_bias IS NULL AND ai_topic IS NOT NULL AND ai_topic != '' GROUP BY ai_topic ORDER BY cnt DESC")
	if err != nil {
		logToFile(fmt.Sprintf("Failed to fetch topics: %v", err))
		return []string{}
	}
	defer rows.Close()

	var topics []string
	for rows.Next() {
		var topic string
		var count int
		if err := rows.Scan(&topic, &count); err == nil {
			topics = append(topics, topic)
		}
	}
	return topics
}

func (m *model) getUniqueStances() []string {
	rows, err := m.db.Query("SELECT ai_political_stance, COUNT(*) as cnt FROM revisions WHERE manual_bias IS NULL AND ai_political_stance IS NOT NULL AND ai_political_stance != '' GROUP BY ai_political_stance ORDER BY cnt DESC")
	if err != nil {
		logToFile(fmt.Sprintf("Failed to fetch stances: %v", err))
		return []string{}
	}
	defer rows.Close()

	var stances []string
	for rows.Next() {
		var stance string
		var count int
		if err := rows.Scan(&stance, &count); err == nil {
			stances = append(stances, stance)
		}
	}
	return stances
}

func (m *model) fetchRevisions() {
	query := `
		SELECT id, original_revid, article_url, user, timestamp, 
		       diff_before, diff_after, change_type, change_desc, 
			   bias_score_before, bias_score_after, bias_delta, 
			   bias_label_before, bias_label_after, ai_topic, ai_political_stance, is_ip 
		FROM revisions 
		WHERE manual_bias IS NULL
	`
	var args []interface{}

	if m.filterDesc != "" && m.filterDesc != "Any" {
		query += " AND change_desc = ?"
		args = append(args, m.filterDesc)
	}

	if m.filterTopic != "" && m.filterTopic != "Any" {
		query += " AND ai_topic = ?"
		args = append(args, m.filterTopic)
	}

	if m.filterStance != "" && m.filterStance != "Any" {
		query += " AND ai_political_stance = ?"
		args = append(args, m.filterStance)
	}

	switch m.currentSort {
	case SortBiasDesc:
		query += " ORDER BY bias_score_after DESC"
	case SortBiasAsc:
		query += " ORDER BY bias_score_after ASC"
	case SortDiffDesc:
		query += " ORDER BY bias_delta DESC"
	case SortDiffAsc:
		query += " ORDER BY bias_delta ASC"
	case SortTimeNewest:
		query += " ORDER BY timestamp DESC"
	case SortTimeOldest:
		query += " ORDER BY timestamp ASC"
	case SortRandom:
		query += " ORDER BY RANDOM()"
	default:
		query += " ORDER BY bias_score_after DESC"
	}

	query += " LIMIT 100" // Fetch batch of 100 to keep memory low

	rows, err := m.db.Query(query, args...)
	if err != nil {
		logToFile(fmt.Sprintf("Query error: %v", err))
		return
	}
	defer rows.Close()
	var newRevisions []Revision
	for rows.Next() {
		var rev Revision
		var isIP int
		if err := rows.Scan(
			&rev.RevisionID, &rev.OriginalRevid, &rev.ArticleURL, &rev.User, &rev.Timestamp,
			&rev.DiffBefore, &rev.DiffAfter, &rev.ChangeType, &rev.ChangeDesc,
			&rev.BiasScoreBefore, &rev.BiasScoreAfter, &rev.BiasDelta,
			&rev.BiasLabelBefore, &rev.BiasLabelAfter, &rev.Topic, &rev.AIPoliticalStance, &isIP,
		); err != nil {
			logToFile(fmt.Sprintf("Scan error: %v", err))
			continue
		}
		rev.IsIP = (isIP == 1)
		newRevisions = append(newRevisions, rev)
	}

	m.unscoredRevisions = newRevisions

	// Reset current revision if any
	if len(m.unscoredRevisions) > 0 {
		m.currentRevision = m.unscoredRevisions[0]
		m.isReady = false
		m.currentStep = 0
		m.choices = m.biasCategories
		m.cursor = 0
		m.viewport.SetContent("Loading...")
		delete(m.diffCache, m.currentRevision.RevisionID)
	} else {
		m.currentRevision = Revision{}
	}
}

func (m model) Init() tea.Cmd {
	var cmds []tea.Cmd
	// Trigger translation for the first revision if exists
	if len(m.unscoredRevisions) > 0 {
		cmds = append(cmds, processDiffCmd(m.currentRevision.RevisionID, m.currentRevision.DiffBefore, m.currentRevision.DiffAfter))
	}
	cmds = append(cmds, tick(time.Millisecond*150))
	return tea.Batch(cmds...)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		
		vpHeight := int(float64(m.height) * 0.35)
		if vpHeight < 5 { vpHeight = 5 }
		m.viewport.Width = m.width - docStyle.GetHorizontalFrameSize()*2 - 4
		m.viewport.Height = vpHeight
		if m.isReady {
			rawContent := m.diffCache[m.currentRevision.RevisionID]
			wrapped := wordwrap.String(rawContent, m.viewport.Width)
			m.viewport.SetContent(wrapped)
		}

	case tickMsg:
		m.birdFrame = birdStanding
		
		if m.statusTimer > 0 {
			m.statusTimer--
			if m.statusTimer == 0 {
				m.statusMessage = ""
			}
		}
		
		return m, tick(time.Millisecond * 150)

	case diffProcessedMsg:
		m.diffCache[msg.id] = msg.content
		if msg.id == m.currentRevision.RevisionID {
			m.isReady = true
			wrapped := wordwrap.String(msg.content, m.viewport.Width)
			m.viewport.SetContent(wrapped)
		}
		return m, nil

	case DashboardTickMsg:
		if m.state == stateDashboard {
			var cmd tea.Cmd
			m.dashboard, cmd = m.dashboard.Update(msg)
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)

	case tea.KeyMsg:
		if m.state == stateDashboard {
			switch msg.String() {
			case "esc", "q", "d":
				m.state = stateReview
				return m, nil
			}
			return m, nil
		}

		if m.state == stateSettings {
			switch msg.String() {
			case "esc":
				m.state = stateReview
				m.statusMessage = "Settings closed."
				m.statusTimer = 20
				return m, nil
			case "up", "k":
				if m.settingsCursor > 0 {
					m.settingsCursor--
				}
			case "down", "j":
				// 0:Sort, 1:Desc, 2:Topic, 3:Stance, 4:Clear, 5:Save&Close
				if m.settingsCursor < 5 {
					m.settingsCursor++
				}
			case "left", "h":
				if m.settingsCursor == 0 { // Sort
					for i, s := range sortOptions {
						if s == m.currentSort {
							if i > 0 {
								m.currentSort = sortOptions[i-1]
							} else {
								m.currentSort = sortOptions[len(sortOptions)-1]
							}
							break
						}
					}
				} else if m.settingsCursor == 1 { // Desc
					allDescs := append([]string{"Any"}, m.uniqueDescs...)
					curr := "Any"
					if m.filterDesc != "" {
						curr = m.filterDesc
					}
					for i, d := range allDescs {
						if d == curr {
							if i > 0 {
								m.filterDesc = allDescs[i-1]
							} else {
								m.filterDesc = allDescs[len(allDescs)-1]
							}
							if m.filterDesc == "Any" {
								m.filterDesc = ""
							}
							break
						}
					}
				} else if m.settingsCursor == 2 { // Topic
					allTopics := append([]string{"Any"}, m.uniqueTopics...)
					curr := "Any"
					if m.filterTopic != "" {
						curr = m.filterTopic
					}
					for i, t := range allTopics {
						if t == curr {
							if i > 0 {
								m.filterTopic = allTopics[i-1]
							} else {
								m.filterTopic = allTopics[len(allTopics)-1]
							}
							if m.filterTopic == "Any" {
								m.filterTopic = ""
							}
							break
						}
					}
				} else if m.settingsCursor == 3 { // Stance
					allStances := append([]string{"Any"}, m.uniqueStances...)
					curr := "Any"
					if m.filterStance != "" {
						curr = m.filterStance
					}
					for i, t := range allStances {
						if t == curr {
							if i > 0 {
								m.filterStance = allStances[i-1]
							} else {
								m.filterStance = allStances[len(allStances)-1]
							}
							if m.filterStance == "Any" {
								m.filterStance = ""
							}
							break
						}
					}
				}
			case "right", "l":
				if m.settingsCursor == 0 { // Sort
					for i, s := range sortOptions {
						if s == m.currentSort {
							if i < len(sortOptions)-1 {
								m.currentSort = sortOptions[i+1]
							} else {
								m.currentSort = sortOptions[0]
							}
							break
						}
					}
				} else if m.settingsCursor == 1 { // Desc
					allDescs := append([]string{"Any"}, m.uniqueDescs...)
					curr := "Any"
					if m.filterDesc != "" {
						curr = m.filterDesc
					}
					for i, d := range allDescs {
						if d == curr {
							if i < len(allDescs)-1 {
								m.filterDesc = allDescs[i+1]
							} else {
								m.filterDesc = allDescs[0]
							}
							if m.filterDesc == "Any" {
								m.filterDesc = ""
							}
							break
						}
					}
				} else if m.settingsCursor == 2 { // Topic
					allTopics := append([]string{"Any"}, m.uniqueTopics...)
					curr := "Any"
					if m.filterTopic != "" {
						curr = m.filterTopic
					}
					for i, t := range allTopics {
						if t == curr {
							if i < len(allTopics)-1 {
								m.filterTopic = allTopics[i+1]
							} else {
								m.filterTopic = allTopics[0]
							}
							if m.filterTopic == "Any" {
								m.filterTopic = ""
							}
							break
						}
					}
				} else if m.settingsCursor == 3 { // Stance
					allStances := append([]string{"Any"}, m.uniqueStances...)
					curr := "Any"
					if m.filterStance != "" {
						curr = m.filterStance
					}
					for i, t := range allStances {
						if t == curr {
							if i < len(allStances)-1 {
								m.filterStance = allStances[i+1]
							} else {
								m.filterStance = allStances[0]
							}
							if m.filterStance == "Any" {
								m.filterStance = ""
							}
							break
						}
					}
				}
			case "enter", " ":
				if m.settingsCursor == 4 { // Clear Labels
					m.shouldClearDB = !m.shouldClearDB
				} else if m.settingsCursor == 5 { // Save & Close
					m.state = stateReview
					m.statusMessage = "Settings applied!"
					m.statusTimer = 20

					if m.shouldClearDB {
						_, err := m.db.Exec("UPDATE revisions SET manual_bias = NULL, manual_topic = NULL")
						if err != nil {
							logToFile(fmt.Sprintf("Failed to clear labels: %v", err))
							m.statusMessage = "Error clearing labels!"
						} else {
							m.statusMessage = "All labels cleared!"
						}
						m.scoredCount = 0
						m.shouldClearDB = false
					}

					m.fetchRevisions()
					if len(m.unscoredRevisions) > 0 {
						cmds = append(cmds, processDiffCmd(m.currentRevision.RevisionID, m.currentRevision.DiffBefore, m.currentRevision.DiffAfter))
					}
				}
			}
			return m, tea.Batch(cmds...)
		}

		// Review State Key Handling
		switch msg.String() {
		case "ctrl+c", "q":
			m.quitting = true
			return m, tea.Quit
		case "s":
			m.state = stateSettings
			m.settingsCursor = 0
			// Remove buildSettingsForm call
			return m, nil
		case "d":
			m.state = stateDashboard
			m.dashboard = NewDashboardModel(m.db, m.width, m.height)
			return m, m.dashboard.Init()
		}

		if m.isReady && len(m.unscoredRevisions) > 0 {
			var vpCmd tea.Cmd
			switch msg.String() {
			case "up":
				if m.cursor > 0 { m.cursor-- }
			case "down":
				if m.cursor < len(m.choices)-1 { m.cursor++ }
			case "j", "k", "pgup", "pgdown":
				m.viewport, vpCmd = m.viewport.Update(msg)
				cmds = append(cmds, vpCmd)
			case "enter", " ":
				selected := m.choices[m.cursor]

				if m.currentStep == 0 {
					m.selectedBias = selected
					m.currentStep = 1
					m.choices = m.topicCategories
					m.cursor = 0
				} else {
					m.selectedTopic = selected
					m.birdFrame = birdJumping
					m.scoredCount++

					_, err := m.db.Exec("UPDATE revisions SET manual_bias = ?, manual_topic = ? WHERE id = ?", m.selectedBias, m.selectedTopic, m.currentRevision.RevisionID)
					if err != nil {
						logToFile(fmt.Sprintf("Error updating revision %v: %v", m.currentRevision.RevisionID, err))
					}

					if len(m.unscoredRevisions) > 0 {
						m.unscoredRevisions = m.unscoredRevisions[1:]
					}

					delete(m.diffCache, m.currentRevision.RevisionID)

					if len(m.unscoredRevisions) == 0 {
						// Try fetching more
						m.fetchRevisions()
					}

					if len(m.unscoredRevisions) == 0 {
						// Still empty after fetch
						return m, nil // Wait or show empty message in View
					}

					m.currentRevision = m.unscoredRevisions[0]
					m.currentStep = 0
					m.choices = m.biasCategories
					m.cursor = 0
					m.selectedBias = ""
					m.selectedTopic = ""

					// Check cache
				if content, ok := m.diffCache[m.currentRevision.RevisionID]; ok {
						m.isReady = true
						wrapped := wordwrap.String(content, m.viewport.Width)
						m.viewport.SetContent(wrapped)
						m.viewport.GotoTop()
					} else {
						m.isReady = false
						m.viewport.SetContent("Loading...")
						cmds = append(cmds, processDiffCmd(m.currentRevision.RevisionID, m.currentRevision.DiffBefore, m.currentRevision.DiffAfter))
					}
					
					// Pre-load next few
					for i := 1; i < 3 && i < len(m.unscoredRevisions); i++ {
						rev := m.unscoredRevisions[i]
						if _, ok := m.diffCache[rev.RevisionID]; !ok {
							cmds = append(cmds, processDiffCmd(rev.RevisionID, rev.DiffBefore, rev.DiffAfter))
						}
					}
					cmds = append(cmds, tick(time.Millisecond*150))
				}
			}
		}
	}

	return m, tea.Batch(cmds...)
}

func (m model) View() string {
	if m.quitting {
		return docStyle.Render("Bye!\n")
	}

	if m.state == stateDashboard {
		return m.dashboard.View()
	}

	if m.state == stateSettings {
		var s strings.Builder
		s.WriteString(titleStyle.Render("Settings") + "\n\n")

		// Helper to render a line
		renderLine := func(i int, label, value string) {
			cursor := "  "
			if m.settingsCursor == i {
				cursor = selectedStyle.Render("> ")
			}
			s.WriteString(fmt.Sprintf("%s%s: %s\n", cursor, label, value))
		}

		// 0: Sort Order
		sortVal := m.currentSort
		if m.settingsCursor == 0 {
			sortVal = fmt.Sprintf("← %s →", sortVal)
		}
		renderLine(0, "Sort Order", sortVal)

		// 1: Description Filter
		descVal := m.filterDesc
		if descVal == "" {
			descVal = "Any (All Descriptions)"
		}
		if len(descVal) > 40 {
			descVal = descVal[:37] + "..."
		}
		if m.settingsCursor == 1 {
			descVal = fmt.Sprintf("← %s →", descVal)
		}
		renderLine(1, "Filter by Description", descVal)

		// 2: Topic Filter
		topicVal := m.filterTopic
		if topicVal == "" {
			topicVal = "Any (All Topics)"
		}
		if m.settingsCursor == 2 {
			topicVal = fmt.Sprintf("← %s →", topicVal)
		}
		renderLine(2, "Filter by AI Topic", topicVal)

		// 3: Stance Filter
		stanceVal := m.filterStance
		if stanceVal == "" {
			stanceVal = "Any (All Stances)"
		}
		if m.settingsCursor == 3 {
			stanceVal = fmt.Sprintf("← %s →", stanceVal)
		}
		renderLine(3, "Filter by AI Stance", stanceVal)

		// 4: Clear DB
		clearVal := "[ ]"
		if m.shouldClearDB {
			clearVal = "[x]"
		}
		renderLine(4, "Clear ALL Manual Labels?", clearVal)

		// 5: Save & Close
		cursor := "  "
		if m.settingsCursor == 5 {
			cursor = selectedStyle.Render("> ")
		}
		s.WriteString(fmt.Sprintf("\n%s%s\n", cursor, "Save & Close"))

		s.WriteString(helpStyle.Render("\nUse ↑/↓ to select, ←/→ to change values, Enter to toggle/save."))
		return docStyle.Render(s.String())
	}

	// Feedback Message
	var feedback string
	if m.statusMessage != "" {
		feedback = successStyle.Render(m.statusMessage) + "\n"
	}

	if len(m.unscoredRevisions) == 0 {
		return docStyle.Render(lipgloss.JoinVertical(lipgloss.Left,
			feedback,
			titleStyle.Render("No revisions match your criteria!"),
			infoStyle.Render("Press 's' to change settings/filters."),
			helpStyle.Render("Press 'q' to quit."),
		))
	}

	// Title
	title := titleStyle.Render("Wikipedia Revision Categorizer")

	// Info
	sentStyle := lipgloss.NewStyle()
	if m.currentRevision.BiasDelta > 0.1 {
		sentStyle = sentStyle.Foreground(lipgloss.Color("160"))
	} else if m.currentRevision.BiasDelta < -0.1 {
		sentStyle = sentStyle.Foreground(lipgloss.Color("42"))
	} else {
		sentStyle = sentStyle.Foreground(lipgloss.Color("244"))
	}

	info := lipgloss.JoinVertical(lipgloss.Left,
		infoStyle.Render(fmt.Sprintf("User: %s  •  Time: %s  •  ID: %s", m.currentRevision.User, m.currentRevision.Timestamp, m.currentRevision.RevisionID)),
		infoStyle.Render(fmt.Sprintf("Desc: %s", m.currentRevision.ChangeDesc)),
		infoStyle.Render(fmt.Sprintf("AI Topic: %s  •  AI Stance: %s", m.currentRevision.Topic, m.currentRevision.AIPoliticalStance)),
		sentStyle.Render(fmt.Sprintf("AI Bias: %.2f (%s) → %.2f (%s) [Delta: %.2f]",
			m.currentRevision.BiasScoreBefore, m.currentRevision.BiasLabelBefore,
			m.currentRevision.BiasScoreAfter, m.currentRevision.BiasLabelAfter,
			m.currentRevision.BiasDelta)),
		infoStyle.Render(fmt.Sprintf("Scored: %d | Filter: D:%.15s / T:%s / S:%s | Sort: %s", m.scoredCount, m.filterDesc, m.filterTopic, m.filterStance, m.currentSort)),
	)

	// Diff Box
	var diffView string
	if !m.isReady {
		diffView = diffBoxStyle.Width(m.width - docStyle.GetHorizontalFrameSize()*2).Render("Translating and formatting diff... Please wait.")
	} else {
		diffView = m.viewport.View()
	}

	// Categories
	var s strings.Builder
	prompt := "Step 1/2: Select Political Bias:"
	if m.currentStep == 1 {
		prompt = "Step 2/2: Select Topic:"
	}
	s.WriteString(titleStyle.Render(prompt) + "\n\n")

	for i, choice := range m.choices {
		cursor := " "
		if m.cursor == i {
			cursor = selectedStyle.Render(">")
		}
		line := fmt.Sprintf("%s %s", cursor, choice)
		if m.cursor == i {
			s.WriteString(selectedStyle.Render(line))
		} else {
			s.WriteString(line)
		}
		s.WriteString("\n")
	}

	// Bird
	bird := birdStyle.Render(m.birdFrame)

	// Help
	help := helpStyle.Render("Use ↑/↓ to select, j/k to scroll diff, Enter to confirm, 's' for Settings, 'd' for Dashboard, 'q' to quit.")

	leftPanel := s.String()
	rightPanel := bird
	content := lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, rightPanel)

	return docStyle.Render(lipgloss.JoinVertical(lipgloss.Left,
		feedback,
		title,
		info,
		diffView,
		content,
		help,
	))
}

// Global translation lock
var translationMutex sync.Mutex

func translateText(text string) (result string) {
	if text == "" {
		return ""
	}
	result = text
	defer func() {
		if r := recover(); r != nil {
			logToFile(fmt.Sprintf("Panic in translateText: %v", r))
		}
	}()
	translationMutex.Lock()
	defer translationMutex.Unlock()
	translated, err := gtranslate.TranslateWithParams(
		text,
		gtranslate.TranslationParams{From: "auto", To: "en"},
	)
	if err != nil {
		logToFile(fmt.Sprintf("Translation error: %v", err))
		return text
	}
	return translated
}

func processDiffCmd(id string, diffBefore, diffAfter string) tea.Cmd {
	return func() tea.Msg {
		defer func() {
			if r := recover(); r != nil {
				logToFile(fmt.Sprintf("Panic in processDiffCmd: %v", r))
			}
		}()
		processed := processDiffContent(diffBefore, diffAfter)
		return diffProcessedMsg{id: id, content: processed}
	}
}

func processDiffContent(before, after string) string {
	tBefore := translateText(before)
	tAfter := translateText(after)
	return renderDiff(tBefore, tAfter)
}

func renderDiff(text1, text2 string) string {
	w1 := strings.Fields(text1)
	w2 := strings.Fields(text2)

	// LCS Dynamic Programming
	n, m := len(w1), len(w2)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}

	for i := 1; i <= n; i++ {
		for j := 1; j <= m; j++ {
			if w1[i-1] == w2[j-1] {
				lcs[i][j] = lcs[i-1][j-1] + 1
			} else {
				if lcs[i-1][j] > lcs[i][j-1] {
					lcs[i][j] = lcs[i-1][j]
				} else {
					lcs[i][j] = lcs[i][j-1]
				}
			}
		}
	}

	// Backtrack to collect operations
	type opCode int
	const (
		opEq opCode = iota
		opDel
		opIns
	)
	type op struct {
		kind opCode
		word string
	}
	
	var ops []op
	i, j := n, m
	for i > 0 || j > 0 {
		if i > 0 && j > 0 && w1[i-1] == w2[j-1] {
			ops = append(ops, op{opEq, w1[i-1]})
			i--
			j--
		} else if j > 0 && (i == 0 || lcs[i][j-1] >= lcs[i-1][j]) {
			ops = append(ops, op{opIns, w2[j-1]})
			j--
		} else {
			ops = append(ops, op{opDel, w1[i-1]})
			i--
		}
	}

	// Reverse ops
	for k := 0; k < len(ops)/2; k++ {
		ops[k], ops[len(ops)-1-k] = ops[len(ops)-1-k], ops[k]
	}

	// Render
	var sb strings.Builder
	
	// Styles
	styleContext := lipgloss.NewStyle().Foreground(lipgloss.Color("240")) // Dim gray
	styleRem := lipgloss.NewStyle().Background(lipgloss.Color("52")).Foreground(lipgloss.Color("196")).Strikethrough(true) // Red
	styleAdd := lipgloss.NewStyle().Background(lipgloss.Color("22")).Foreground(lipgloss.Color("46")) // Green

	for _, o := range ops {
		switch o.kind {
		case opEq:
			sb.WriteString(styleContext.Render(o.word) + " ")
		case opDel:
			sb.WriteString(styleRem.Render(o.word) + " ")
		case opIns:
			sb.WriteString(styleAdd.Render(o.word) + " ")
		}
	}

	return sb.String()
}

func logToFile(message string) {
	f, err := os.OpenFile("debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Println(err)
	}
	defer f.Close()
	if _, err := f.WriteString(message + "\n"); err != nil {
		log.Println(err)
	}
}

func tick(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func main() {
	if _, err := os.Stat("debug.log"); err == nil {
		os.Remove("debug.log")
	}
	rand.Seed(time.Now().UnixNano())

	db, err := sql.Open("sqlite3", DB_PATH)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Load bias categories
	biasData, err := ioutil.ReadFile("data/political_categories.json")
	if err != nil {
		log.Fatalf("Failed to read political_categories.json: %v", err)
	}
	var biasCategories []string
	if err := json.Unmarshal(biasData, &biasCategories); err != nil {
		log.Fatalf("Failed to unmarshal political_categories.json: %v", err)
	}

	// Load topic categories
	topicData, err := ioutil.ReadFile("data/topic_categories.json")
	if err != nil {
		log.Fatalf("Failed to read topic_categories.json: %v", err)
	}
	var topicCategories []string
	if err := json.Unmarshal(topicData, &topicCategories); err != nil {
		log.Fatalf("Failed to unmarshal topic_categories.json: %v", err)
	}

	var initialScoredCount int
	row := db.QueryRow("SELECT COUNT(*) FROM revisions WHERE manual_bias IS NOT NULL")
	if err := row.Scan(&initialScoredCount); err != nil {
		initialScoredCount = 0
	}

	// Model initialization handles the initial fetch
	m := newModel(db, biasCategories, topicCategories, initialScoredCount)
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Error: %v", err)
		os.Exit(1)
	}
}
