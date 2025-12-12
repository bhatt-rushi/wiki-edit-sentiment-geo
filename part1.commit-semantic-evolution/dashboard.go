package main

import (
	"database/sql"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/NimbleMarkets/ntcharts/barchart"
	"github.com/NimbleMarkets/ntcharts/linechart"
	"github.com/NimbleMarkets/ntcharts/canvas"
)

type DashboardTickMsg time.Time

type DashboardModel struct {
	db              *sql.DB
	topicChart      barchart.Model
	biasChart       barchart.Model
	biasLineChart   linechart.Model
	commitLineChart linechart.Model
	width           int
	height          int
	loaded          bool
}

func NewDashboardModel(db *sql.DB, width, height int) DashboardModel {
	m := DashboardModel{
		db:     db,
		width:  width,
		height: height,
	}
	m.loadData()
	return m
}

func (m *DashboardModel) loadData() {
	halfW := m.width/2 - 6
	halfH := m.height/2 - 6

	// Styles
	axisStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("252"))

	// 1. Topic Distribution
	topicData := m.fetchTopicDistribution()
	m.topicChart = barchart.New(halfW, halfH, 
		barchart.WithDataSet(topicData),
		barchart.WithStyles(axisStyle, labelStyle),
	)
	m.topicChart.Draw()

	// 2. Bias Distribution
	biasData := m.fetchBiasDistribution()
	m.biasChart = barchart.New(halfW, halfH, 
		barchart.WithDataSet(biasData),
		barchart.WithStyles(axisStyle, labelStyle),
	)
	m.biasChart.Draw()

	// 3. Bias Over Time
	biasDates, biasVals := m.fetchBiasOverTime()
	minY, maxY := getMinMax(biasVals)
	
	biasXFormatter := func(i int, x float64) string {
		idx := int(x)
		if idx >= 0 && idx < len(biasDates) {
			return biasDates[idx]
		}
		return ""
	}

	m.biasLineChart = linechart.New(halfW, halfH, 0, float64(len(biasVals)-1), minY, maxY, linechart.WithXLabelFormatter(biasXFormatter))
	for i := 0; i < len(biasVals)-1; i++ {
		p1 := canvas.Float64Point{X: float64(i), Y: biasVals[i]}
		p2 := canvas.Float64Point{X: float64(i + 1), Y: biasVals[i+1]}
		m.biasLineChart.DrawBrailleLine(p1, p2)
	}
	m.biasLineChart.DrawXYAxisAndLabel()

	// 4. Commit Frequency
	commitDates, commitVals := m.fetchCommitFrequency()
	minY, maxY = getMinMax(commitVals)

	commitXFormatter := func(i int, x float64) string {
		idx := int(x)
		if idx >= 0 && idx < len(commitDates) {
			return commitDates[idx]
		}
		return ""
	}

	m.commitLineChart = linechart.New(halfW, halfH, 0, float64(len(commitVals)-1), minY, maxY, linechart.WithXLabelFormatter(commitXFormatter))
	for i := 0; i < len(commitVals)-1; i++ {
		p1 := canvas.Float64Point{X: float64(i), Y: commitVals[i]}
		p2 := canvas.Float64Point{X: float64(i + 1), Y: commitVals[i+1]}
		m.commitLineChart.DrawBrailleLine(p1, p2)
	}
	m.commitLineChart.DrawXYAxisAndLabel()

	m.loaded = true
}

func getMinMax(vals []float64) (min, max float64) {
	if len(vals) == 0 {
		return 0, 1
	}
	min, max = vals[0], vals[0]
	for _, v := range vals {
		if v < min { min = v }
		if v > max { max = v }
	}
	if min == max { max += 1 } // Prevent flat line range error
	return
}

func (m *DashboardModel) fetchTopicDistribution() []barchart.BarData {
	rows, err := m.db.Query("SELECT ai_topic, COUNT(*) FROM revisions GROUP BY ai_topic ORDER BY COUNT(*) DESC LIMIT 10")
	if err != nil {
		logToFile(fmt.Sprintf("Error fetching topics: %v", err))
		return nil
	}
	defer rows.Close()

	var data []barchart.BarData
	for rows.Next() {
		var topic string
		var count int
		if err := rows.Scan(&topic, &count); err == nil {
			if len(topic) > 10 { topic = topic[:10] + ".." }
			data = append(data, barchart.BarData{
				Label: topic, 
				Values: []barchart.BarValue{{Value: float64(count)}},
			})
		}
	}
	logToFile(fmt.Sprintf("DEBUG: Found %d topics", len(data)))
	return data
}

func (m *DashboardModel) fetchBiasDistribution() []barchart.BarData {
	bins := make([]int, 5)
	rows, err := m.db.Query("SELECT bias_score_after FROM revisions")
	if err != nil {
		logToFile(fmt.Sprintf("Error fetching bias scores: %v", err))
		return nil
	}
	defer rows.Close()

	for rows.Next() {
		var score float64
		if err := rows.Scan(&score); err == nil {
			idx := int(score * 5)
			if idx >= 5 { idx = 4 }
			if idx < 0 { idx = 0 }
			bins[idx]++
		}
	}

	labels := []string{"0-.2", ".2-.4", ".4-.6", ".6-.8", ".8-1"}
	var data []barchart.BarData
	for i, count := range bins {
		data = append(data, barchart.BarData{
			Label: labels[i], 
			Values: []barchart.BarValue{{Value: float64(count)}},
		})
	}
	logToFile(fmt.Sprintf("DEBUG: Found %d bias bins", len(data)))
	return data
}

func (m *DashboardModel) fetchBiasOverTime() ([]string, []float64) {
	rows, err := m.db.Query("SELECT date(timestamp), AVG(bias_score_after) FROM revisions GROUP BY date(timestamp) ORDER BY date(timestamp) ASC")
	if err != nil {
		logToFile(fmt.Sprintf("Error fetching bias/time: %v", err))
		return []string{}, []float64{0}
	}
	defer rows.Close()

	var dates []string
	var vals []float64
	for rows.Next() {
		var d string
		var val float64
		if err := rows.Scan(&d, &val); err == nil {
			dates = append(dates, d)
			vals = append(vals, val)
		}
	}
	logToFile(fmt.Sprintf("DEBUG: Found %d bias time points", len(vals)))
	if len(vals) == 0 { return []string{}, []float64{0} }
	return dates, vals
}

func (m *DashboardModel) fetchCommitFrequency() ([]string, []float64) {
	rows, err := m.db.Query("SELECT date(timestamp), COUNT(*) FROM revisions GROUP BY date(timestamp) ORDER BY date(timestamp) ASC")
	if err != nil {
		logToFile(fmt.Sprintf("Error fetching commits/time: %v", err))
		return []string{}, []float64{0}
	}
	defer rows.Close()

	var dates []string
	var vals []float64
	for rows.Next() {
		var d string
		var val float64
		if err := rows.Scan(&d, &val); err == nil {
			dates = append(dates, d)
			vals = append(vals, val)
		}
	}
	logToFile(fmt.Sprintf("DEBUG: Found %d commit time points", len(vals)))
	if len(vals) == 0 { return []string{}, []float64{0} }
	return dates, vals
}

func (m DashboardModel) Init() tea.Cmd {
	return tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
		return DashboardTickMsg(t)
	})
}

func (m DashboardModel) Update(msg tea.Msg) (DashboardModel, tea.Cmd) {
	switch msg.(type) {
	case DashboardTickMsg:
		m.loadData()
		return m, tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
			return DashboardTickMsg(t)
		})
	}
	return m, nil
}

func (m DashboardModel) View() string {
	if !m.loaded {
		return "Loading Dashboard..."
	}
	
	boxStyle := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)

	row1 := lipgloss.JoinHorizontal(lipgloss.Top,
		lipgloss.JoinVertical(lipgloss.Left, "Topic Distribution", boxStyle.Render(m.topicChart.View())),
		lipgloss.JoinVertical(lipgloss.Left, "Bias Distribution", boxStyle.Render(m.biasChart.View())),
	)
	
	row2 := lipgloss.JoinHorizontal(lipgloss.Top,
		lipgloss.JoinVertical(lipgloss.Left, "Bias Score Over Time", boxStyle.Render(m.biasLineChart.View())),
		lipgloss.JoinVertical(lipgloss.Left, "Commit Frequency", boxStyle.Render(m.commitLineChart.View())),
	)

	return lipgloss.JoinVertical(lipgloss.Left, row1, row2)
}