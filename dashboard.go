package main

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/NimbleMarkets/ntcharts/barchart"
	"github.com/NimbleMarkets/ntcharts/canvas"
	"github.com/NimbleMarkets/ntcharts/linechart"
	tslc "github.com/NimbleMarkets/ntcharts/linechart/timeserieslinechart"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type DashboardTickMsg time.Time

var lineColors = []lipgloss.Color{
	lipgloss.Color("4"),   // Blue
	lipgloss.Color("5"),   // Magenta
	lipgloss.Color("6"),   // Cyan
	lipgloss.Color("2"),   // Green
	lipgloss.Color("3"),   // Yellow
	lipgloss.Color("1"),   // Red
	lipgloss.Color("202"), // Orange
	lipgloss.Color("213"), // Pink
}

type DashboardModel struct {
	db              *sql.DB
	topicChart      barchart.Model
	biasChart       barchart.Model
	stanceChart     barchart.Model
	biasLineChart   linechart.Model
	topicLineChart  tslc.Model
	stanceLineChart tslc.Model
	topicLegend     string
	stanceLegend    string
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
	if m.width == 0 || m.height == 0 {
		return
	}

	chartW := m.width/2 - 8
	chartH := m.height/3 - 5
	if chartW < 10 {
		chartW = 10
	}
	if chartH < 5 {
		chartH = 5
	}

	axisStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("252"))

	// Bar charts
	m.topicChart = barchart.New(chartW, chartH, barchart.WithDataSet(m.fetchTopicDistribution()), barchart.WithStyles(axisStyle, labelStyle))
	m.topicChart.Draw()
	m.biasChart = barchart.New(chartW, chartH, barchart.WithDataSet(m.fetchBiasDistribution()), barchart.WithStyles(axisStyle, labelStyle))
	m.biasChart.Draw()
	m.stanceChart = barchart.New(chartW, chartH, barchart.WithDataSet(m.fetchStanceDistribution()), barchart.WithStyles(axisStyle, labelStyle))
	m.stanceChart.Draw()

	// Bias Line Chart
	biasDates, biasVals := m.fetchBiasOverTime()
	minY, maxY := getMinMax(biasVals)
	m.biasLineChart = linechart.New(chartW, chartH, 0, float64(len(biasVals)-1), minY, maxY, linechart.WithXLabelFormatter(createLabelFormatter(biasDates)))
	for i := 0; i < len(biasVals)-1; i++ {
		p1 := canvas.Float64Point{X: float64(i), Y: biasVals[i]}
		p2 := canvas.Float64Point{X: float64(i + 1), Y: biasVals[i+1]}
		m.biasLineChart.DrawBrailleLine(p1, p2)
	}
	m.biasLineChart.DrawXYAxisAndLabel()

	// Topic Over Time (TimeSeriesLineChart)
	m.topicLineChart = tslc.New(chartW, chartH)
	topicDates, topicValsMap := m.fetchTopicOverTime()
	topics := make([]string, 0, len(topicValsMap))
	for k := range topicValsMap {
		topics = append(topics, k)
	}
	sort.Strings(topics)
	var topicLegend strings.Builder
	topicLegend.WriteString("Legend: ")
	colorIndex := 0
	for _, topic := range topics {
		style := lipgloss.NewStyle().Foreground(lineColors[colorIndex%len(lineColors)])
		topicLegend.WriteString(style.Render(fmt.Sprintf("■ %s  ", topic)))
		m.topicLineChart.SetDataSetStyle(topic, style)
		vals := topicValsMap[topic]
		for i, dateStr := range topicDates {
			if i < len(vals) {
				t, err := time.Parse("2006-01-02", dateStr)
				if err == nil {
					m.topicLineChart.PushDataSet(topic, tslc.TimePoint{Time: t, Value: vals[i]})
				}
			}
		}
		colorIndex++
	}
	m.topicLineChart.DrawBrailleAll()
	m.topicLegend = topicLegend.String()

	// Stance Over Time (TimeSeriesLineChart)
	m.stanceLineChart = tslc.New(chartW, chartH)
	stanceDates, stanceValsMap := m.fetchStanceOverTime()
	stances := make([]string, 0, len(stanceValsMap))
	for k := range stanceValsMap {
		stances = append(stances, k)
	}
	sort.Strings(stances)
	var stanceLegend strings.Builder
	stanceLegend.WriteString("Legend: ")
	colorIndex = 0
	for _, stance := range stances {
		style := lipgloss.NewStyle().Foreground(lineColors[colorIndex%len(lineColors)])
		stanceLegend.WriteString(style.Render(fmt.Sprintf("■ %s  ", stance)))
		m.stanceLineChart.SetDataSetStyle(stance, style)
		vals := stanceValsMap[stance]
		for i, dateStr := range stanceDates {
			if i < len(vals) {
				t, err := time.Parse("2006-01-02", dateStr)
				if err == nil {
					m.stanceLineChart.PushDataSet(stance, tslc.TimePoint{Time: t, Value: vals[i]})
				}
			}
		}
		colorIndex++
	}
	m.stanceLineChart.DrawBrailleAll()
	m.stanceLegend = stanceLegend.String()

	m.loaded = true
}

func createLabelFormatter(labels []string) func(int, float64) string {
	return func(i int, x float64) string {
		idx := int(x)
		if idx >= 0 && idx < len(labels) {
			return labels[idx]
		}
		return ""
	}
}

func getMinMax(vals []float64) (min, max float64) {
	if len(vals) == 0 {
		return 0, 1
	}
	min, max = vals[0], vals[0]
	for _, v := range vals {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	if min == max {
		max += 1
	}
	return
}

func (m *DashboardModel) fetchTopicDistribution() []barchart.BarData {
	rows, err := m.db.Query("SELECT ai_topic, COUNT(*) FROM revisions WHERE ai_topic IS NOT NULL AND ai_topic != '' GROUP BY ai_topic ORDER BY COUNT(*) DESC LIMIT 10")
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
			if len(topic) > 10 {
				topic = topic[:10] + ".."
			}
			data = append(data, barchart.BarData{Label: topic, Values: []barchart.BarValue{{Value: float64(count)}}})
		}
	}
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
			if idx >= 5 {
				idx = 4
			}
			if idx < 0 {
				idx = 0
			}
			bins[idx]++
		}
	}
	labels := []string{"0-.2", ".2-.4", ".4-.6", ".6-.8", ".8-1"}
	var data []barchart.BarData
	for i, count := range bins {
		data = append(data, barchart.BarData{Label: labels[i], Values: []barchart.BarValue{{Value: float64(count)}}})
	}
	return data
}

func (m *DashboardModel) fetchStanceDistribution() []barchart.BarData {
	rows, err := m.db.Query("SELECT ai_political_stance, COUNT(*) FROM revisions WHERE ai_political_stance IS NOT NULL AND ai_political_stance != '' GROUP BY ai_political_stance ORDER BY COUNT(*) DESC LIMIT 10")
	if err != nil {
		logToFile(fmt.Sprintf("Error fetching stances: %v", err))
		return nil
	}
	defer rows.Close()
	var data []barchart.BarData
	for rows.Next() {
		var stance string
		var count int
		if err := rows.Scan(&stance, &count); err == nil {
			if len(stance) > 10 {
				stance = stance[:10] + ".."
			}
			data = append(data, barchart.BarData{Label: stance, Values: []barchart.BarValue{{Value: float64(count)}}})
		}
	}
	return data
}

func (m *DashboardModel) fetchBiasOverTime() ([]string, []float64) {
	rows, err := m.db.Query("SELECT date(timestamp), AVG(bias_score_after) FROM revisions GROUP BY date(timestamp) ORDER BY date(timestamp) ASC")
	if err != nil {
		logToFile(fmt.Sprintf("Error fetching bias/time: %v", err))
		return []string{""}, []float64{0}
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
	if len(vals) == 0 {
		return []string{""}, []float64{0}
	}
	return dates, vals
}

func (m *DashboardModel) fetchTopicOverTime() ([]string, map[string][]float64) {
	dateRows, err := m.db.Query(`SELECT DISTINCT date(timestamp) FROM revisions WHERE timestamp IS NOT NULL ORDER BY date(timestamp) ASC`)
	if err != nil {
		logToFile(fmt.Sprintf("Error fetching unique dates for topic trend: %v", err))
		return nil, nil
	}
	var dates []string
	for dateRows.Next() {
		var d string
		if err := dateRows.Scan(&d); err == nil {
			dates = append(dates, d)
		}
	}
	dateRows.Close()
	if len(dates) == 0 {
		return nil, nil
	}

	topicRows, err := m.db.Query(`SELECT DISTINCT ai_topic FROM revisions WHERE ai_topic IS NOT NULL AND ai_topic != ''`)
	if err != nil {
		logToFile(fmt.Sprintf("Error fetching unique topics for trend: %v", err))
		return dates, nil
	}
	var topics []string
	for topicRows.Next() {
		var s string
		if err := topicRows.Scan(&s); err == nil {
			topics = append(topics, s)
		}
	}
	topicRows.Close()

	topicData := make(map[string]map[string]float64)
	for _, s := range topics {
		topicData[s] = make(map[string]float64)
	}

	countRows, err := m.db.Query(`
		SELECT date(timestamp), ai_topic, COUNT(*)
		FROM revisions
		WHERE ai_topic IS NOT NULL AND ai_topic != ''
		GROUP BY date(timestamp), ai_topic
	`)
	if err != nil {
		logToFile(fmt.Sprintf("Error fetching topic counts over time: %v", err))
		return dates, nil
	}
	defer countRows.Close()
	for countRows.Next() {
		var date, topic string
		var count float64
		if err := countRows.Scan(&date, &topic, &count); err == nil {
			if _, ok := topicData[topic]; ok {
				topicData[topic][date] = count
			}
		}
	}

	finalData := make(map[string][]float64)
	for _, s := range topics {
		counts := make([]float64, len(dates))
		for i, d := range dates {
			counts[i] = topicData[s][d] // Defaults to 0 if not found
		}
		finalData[s] = counts
	}
	return dates, finalData
}

func (m *DashboardModel) fetchStanceOverTime() ([]string, map[string][]float64) {
	dateRows, err := m.db.Query(`SELECT DISTINCT date(timestamp) FROM revisions WHERE timestamp IS NOT NULL ORDER BY date(timestamp) ASC`)
	if err != nil {
		logToFile(fmt.Sprintf("Error fetching unique dates for stance trend: %v", err))
		return nil, nil
	}
	var dates []string
	for dateRows.Next() {
		var d string
		if err := dateRows.Scan(&d); err == nil {
			dates = append(dates, d)
		}
	}
	dateRows.Close()
	if len(dates) == 0 {
		return nil, nil
	}

	stanceRows, err := m.db.Query(`SELECT DISTINCT ai_political_stance FROM revisions WHERE ai_political_stance IS NOT NULL AND ai_political_stance != ''`)
	if err != nil {
		logToFile(fmt.Sprintf("Error fetching unique stances for stance trend: %v", err))
		return dates, nil
	}
	var stances []string
	for stanceRows.Next() {
		var s string
		if err := stanceRows.Scan(&s); err == nil {
			stances = append(stances, s)
		}
	}
	stanceRows.Close()

	stanceData := make(map[string]map[string]float64)
	for _, s := range stances {
		stanceData[s] = make(map[string]float64)
	}

	countRows, err := m.db.Query(`
		SELECT date(timestamp), ai_political_stance, COUNT(*)
		FROM revisions
		WHERE ai_political_stance IS NOT NULL AND ai_political_stance != ''
		GROUP BY date(timestamp), ai_political_stance
	`)
	if err != nil {
		logToFile(fmt.Sprintf("Error fetching stance counts over time: %v", err))
		return dates, nil
	}
	defer countRows.Close()
	for countRows.Next() {
		var date, stance string
		var count float64
		if err := countRows.Scan(&date, &stance, &count); err == nil {
			if _, ok := stanceData[stance]; ok {
				stanceData[stance][date] = count
			}
		}
	}

	finalData := make(map[string][]float64)
	for _, s := range stances {
		counts := make([]float64, len(dates))
		for i, d := range dates {
			counts[i] = stanceData[s][d] // Defaults to 0 if not found
		}
		finalData[s] = counts
	}
	return dates, finalData
}

func (m DashboardModel) Init() tea.Cmd {
	return tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
		return DashboardTickMsg(t)
	})
}

func (m DashboardModel) Update(msg tea.Msg) (DashboardModel, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.loadData()
	case DashboardTickMsg:
		m.loadData()
		return m, tea.Tick(3*time.Second, func(t time.Time) tea.Msg { return DashboardTickMsg(t) })
	}

	m.topicLineChart, cmd = m.topicLineChart.Update(msg)
	m.stanceLineChart, cmd = m.stanceLineChart.Update(msg)
	return m, cmd
}

func (m DashboardModel) View() string {
	if !m.loaded || m.width == 0 {
		return "Loading Dashboard..."
	}

	boxStyle := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)

	viewTopicDist := lipgloss.JoinVertical(lipgloss.Left, "Topic Distribution (Count)", boxStyle.Render(m.topicChart.View()))
	viewBiasDist := lipgloss.JoinVertical(lipgloss.Left, "Bias Distribution (Count)", boxStyle.Render(m.biasChart.View()))
	viewStanceDist := lipgloss.JoinVertical(lipgloss.Left, "Stance Distribution (Count)", boxStyle.Render(m.stanceChart.View()))
	viewBiasTrend := lipgloss.JoinVertical(lipgloss.Left, "Bias Score Over Time", boxStyle.Render(m.biasLineChart.View()))
	viewTopicTrend := lipgloss.JoinVertical(lipgloss.Left, "Topic Trend Over Time", boxStyle.Render(m.topicLineChart.View()), m.topicLegend)
	viewStanceTrend := lipgloss.JoinVertical(lipgloss.Left, "Stance Trend Over Time", boxStyle.Render(m.stanceLineChart.View()), m.stanceLegend)

	row1 := lipgloss.JoinHorizontal(lipgloss.Top, viewTopicDist, viewBiasDist)
	row2 := lipgloss.JoinHorizontal(lipgloss.Top, viewStanceDist, viewBiasTrend)
	row3 := lipgloss.JoinHorizontal(lipgloss.Top, viewTopicTrend, viewStanceTrend)

	return lipgloss.JoinVertical(lipgloss.Left, row1, row2, row3)
}
