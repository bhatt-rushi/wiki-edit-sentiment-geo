package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/net/html"
)

type tickMsg time.Time

// Styles
var (
	docStyle        = lipgloss.NewStyle().Margin(1, 2)
	titleStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("63")).Bold(true)
	infoStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	helpStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	diffBoxStyle    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("63")).Padding(1, 2)
	selectedStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Bold(true)
	birdStyle       = lipgloss.NewStyle().PaddingLeft(2)
)

// Bird frames
var (
	birdStanding = `>('.')<`
	birdJumping  = `>('o')<`
)

// Revision represents a single revision entry in the JSON
type Revision struct {
	RevisionID int    `json:"revision_id"`
	User       string `json:"user"`
	Timestamp  string `json:"timestamp"`
	Diff       string `json:"diff"`
	IsIP       bool   `json:"is_ip"`
	Country    string `json:"country"`
	GeoCoords  struct {
		Lat float64 `json:"lat"`
		Lon float64 `json:"lon"`
	} `json:"geo_coords"`
}

// Article represents an article with its revisions
type Article struct {
	ArticleURL string     `json:"article_url"`
	Revisions  []Revision `json:"revisions"`
}

// CategorizedRevision represents a revision that has been categorized
type CategorizedRevision struct {
	RevisionID string `json:"revision_id"`
	Category   string `json:"category"`
}

// Model represents the Bubble Tea model
type model struct {
	unscoredRevisions []Revision
	categories        []string
	currentRevision   Revision
	selectedCategory  string
	choices           []string // Items on the to-do list
	cursor            int      // which to-do list item our cursor is pointing at
	quitting          bool
	width, height     int
	birdFrame         string
	scoredCount       int
}

func newModel(unscored []Revision, cats []string, scoredCount int) model {
	// Select a random revision
	randomIndex := rand.Intn(len(unscored))
	randomRevision := unscored[randomIndex]

	return model{
		unscoredRevisions: unscored,
		categories:        cats,
		currentRevision:   randomRevision,
		choices:           cats,
		birdFrame:         birdStanding,
		scoredCount:       scoredCount,
	}
}

func (m model) Init() tea.Cmd {
	return tick(time.Millisecond * 150)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tickMsg:
		m.birdFrame = birdStanding
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			m.quitting = true
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.choices)-1 {
				m.cursor++
			}
		case "enter", " ":
			// User selected a category
			m.selectedCategory = m.choices[m.cursor]
			m.birdFrame = birdJumping
			m.scoredCount++

			// Save categorization to file
			categorizedEntry := CategorizedRevision{
				RevisionID: fmt.Sprintf("%d", m.currentRevision.RevisionID),
				Category:   m.selectedCategory,
			}

			var existingEntries []CategorizedRevision
			if _, err := os.Stat("data/categorized_revisions.json"); err == nil {
				fileContent, err := ioutil.ReadFile("data/categorized_revisions.json")
				if err != nil {
					log.Printf("Error reading categorized_revisions.json: %v", err)
				} else {
					if len(fileContent) > 0 {
						if err := json.Unmarshal(fileContent, &existingEntries); err != nil {
							log.Printf("Error unmarshaling categorized_revisions.json: %v", err)
							existingEntries = []CategorizedRevision{} // Reset if unmarshal fails
						}
					}
				}
			}

			existingEntries = append(existingEntries, categorizedEntry)

			output, err := json.MarshalIndent(existingEntries, "", "  ")
			if err != nil {
				log.Printf("Error marshaling categorized entry: %v", err)
			} else {
				if err := ioutil.WriteFile("data/categorized_revisions.json", output, 0644); err != nil {
					log.Printf("Error writing to categorized_revisions.json: %v", err)
				}
			}

			// Remove the just-categorized revision from the unscored list
			var updatedUnscoredRevisions []Revision
			for _, rev := range m.unscoredRevisions {
				if rev.RevisionID != m.currentRevision.RevisionID {
					updatedUnscoredRevisions = append(updatedUnscoredRevisions, rev)
				}
			}
			m.unscoredRevisions = updatedUnscoredRevisions

			if len(m.unscoredRevisions) == 0 {
				m.quitting = true
				return m, tea.Quit
			}

			// Select a new random revision
			randomIndex := rand.Intn(len(m.unscoredRevisions))
			m.currentRevision = m.unscoredRevisions[randomIndex]
			m.cursor = 0

			return m, tick(time.Millisecond * 150)
		}
	}
	return m, nil
}

func (m model) View() string {
	if m.quitting {
		if len(m.unscoredRevisions) == 0 {
			return docStyle.Render("No more unscored revisions. Thanks for your help!\n")
		}
		return docStyle.Render("Bye!\n")
	}

	// Title
	title := titleStyle.Render("Wikipedia Revision Categorizer")

	// Diff Box
	diffContent := formatDiff(m.currentRevision.Diff)
	diffBox := diffBoxStyle.Width(m.width - docStyle.GetHorizontalFrameSize()*2).Render(diffContent)

	// Info
	info := infoStyle.Render(fmt.Sprintf("User: %s  •  Timestamp: %s  •  Revision ID: %d  •  Scored: %d",
		m.currentRevision.User, m.currentRevision.Timestamp, m.currentRevision.RevisionID, m.scoredCount))

	// Categories
	var s strings.Builder
	s.WriteString("Select a category:\n\n")
	for i, choice := range m.choices {
		cursor := " " // no cursor
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
	help := helpStyle.Render("Use ↑/↓ to navigate, ←/→ to select, 'q' to quit.")

	// Layout
	leftPanel := s.String()
	rightPanel := bird
	content := lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, rightPanel)

	return docStyle.Render(lipgloss.JoinVertical(lipgloss.Left,
		title,
		info,
		diffBox,
		content,
		help,
	))
}

func formatDiff(diff string) string {
	logToFile("Formatting diff: " + diff)
	if diff == "N/A" {
		return "No diff available for this revision."
	}

	// The diff is a table fragment. We must parse it within the context of a table.
	// A <tr> fragment should be put in a <table>.
	context, err := html.Parse(strings.NewReader("<table></table>"))
	if err != nil {
		return "Error: Could not create parsing context."
	}
	tableNode := context.FirstChild.LastChild.FirstChild // html -> body -> table

	nodes, err := html.ParseFragment(strings.NewReader(diff), tableNode)
	if err != nil {
		return "Error: Could not parse diff HTML fragment."
	}

	var result strings.Builder
	var traverse func(*html.Node)

	traverse = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "td" {
			var classVal string
			for _, a := range n.Attr {
				if a.Key == "class" {
					classVal = a.Val
					break
				}
			}

			isDeleted := strings.Contains(classVal, "diff-deletedline")
			isAdded := strings.Contains(classVal, "diff-addedline")

			if isDeleted || isAdded {
				var textContent strings.Builder
				var extractText func(*html.Node)
				extractText = func(innerNode *html.Node) {
					if innerNode.Type == html.TextNode {
						textContent.WriteString(innerNode.Data)
					}
					for c := innerNode.FirstChild; c != nil; c = c.NextSibling {
						extractText(c)
					}
				}
				extractText(n)

				line := strings.TrimSpace(textContent.String())
				if len(line) > 0 {
					var style lipgloss.Style
					var prefix string
					if isDeleted {
						style = lipgloss.NewStyle().Background(lipgloss.Color("#5c2424")).Padding(0, 1)
						prefix = "- "
					} else { // isAdded
						style = lipgloss.NewStyle().Background(lipgloss.Color("#245c24")).Padding(0, 1)
						prefix = "+ "
					}
					result.WriteString(style.Render(prefix+line) + "\n")
				}
			}
		}

		for c := n.FirstChild; c != nil; c = c.NextSibling {
			traverse(c)
		}
	}

	for _, node := range nodes {
		traverse(node)
	}

	finalResult := strings.TrimSpace(result.String())
	if finalResult == "" {
		return "Could not extract diff content from HTML."
	}

	return finalResult
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
	// Clean debug log on start
	if _, err := os.Stat("debug.log"); err == nil {
		os.Remove("debug.log")
	}

	// Seed the random number generator
	rand.Seed(time.Now().UnixNano())

	// Load revisions
	revisionsData, err := ioutil.ReadFile("data/revisions.json")
	if err != nil {
		log.Fatalf("Failed to read revisions.json: %v", err)
	}
	var allArticles []Article
	if err := json.Unmarshal(revisionsData, &allArticles); err != nil {
		log.Fatalf("Failed to unmarshal revisions.json: %v", err)
	}

	// Load categories
	categoriesData, err := ioutil.ReadFile("data/categories.json")
	if err != nil {
		log.Fatalf("Failed to read categories.json: %v", err)
	}
	var categories []string
	if err := json.Unmarshal(categoriesData, &categories); err != nil {
		log.Fatalf("Failed to unmarshal categories.json: %v", err)
	}

	// Load categorized revisions
	var categorizedRevisions []CategorizedRevision
	if _, err := os.Stat("data/categorized_revisions.json"); err == nil {
		categorizedData, err := ioutil.ReadFile("data/categorized_revisions.json")
		if err != nil {
			log.Printf("Error reading categorized_revisions.json: %v", err)
		} else {
			if len(categorizedData) > 0 {
				if err := json.Unmarshal(categorizedData, &categorizedRevisions); err != nil {
					log.Printf("Error unmarshaling categorized_revisions.json: %v", err)
				}
			}
		}
	}

	// Create a set of scored revision IDs
	scoredRevisionIDs := make(map[int]bool)
	for _, rev := range categorizedRevisions {
		var revID int
		fmt.Sscanf(rev.RevisionID, "%d", &revID)
		scoredRevisionIDs[revID] = true
	}

	// Flatten all revisions into a single slice and filter out scored revisions
	var unscoredRevisions []Revision
	for _, article := range allArticles {
		for _, rev := range article.Revisions {
			if !scoredRevisionIDs[rev.RevisionID] {
				unscoredRevisions = append(unscoredRevisions, rev)
			}
		}
	}

	if len(unscoredRevisions) == 0 {
		fmt.Println("No unscored revisions found.")
		os.Exit(0)
	}

	m := newModel(unscoredRevisions, categories, len(categorizedRevisions))
	p := tea.NewProgram(m, tea.WithAltScreen())

	if _, err := p.Run(); err != nil {
		log.Fatalf("Alas, there's been an error: %v", err)
	}
}