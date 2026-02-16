package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type runSummary struct {
	RunID              string           `json:"run_id"`
	ExperimentName     string           `json:"experiment_name"`
	OperationMode      string           `json:"operation_mode"`
	Completed          int64            `json:"completed"`
	Failed             int64            `json:"failed"`
	Retries            int64            `json:"retries"`
	FailureStageCounts map[string]int64 `json:"failure_stage_counts"`
	FailureCodeCounts  map[string]int64 `json:"failure_code_counts"`
	OperationCounts    map[string]int64 `json:"operation_counts"`
	CorrectnessSampled int64            `json:"correctness_sampled"`
	CorrectnessPassed  int64            `json:"correctness_passed"`
	CorrectnessFailed  int64            `json:"correctness_failed"`
}

type runRow struct {
	RunID               string
	ExperimentName      string
	OperationMode       string
	Completed           int64
	Failed              int64
	Retries             int64
	SuccessRate         float64
	CorrectnessSampled  int64
	CorrectnessPassed   int64
	CorrectnessFailed   int64
	CorrectnessPassRate float64
	RunDir              string
}

type aggRow struct {
	ExperimentName             string
	OperationMode              string
	Runs                       int64
	TotalCompleted             int64
	TotalFailed                int64
	OverallSuccessRate         float64
	MeanSuccessRate            float64
	TotalRetries               int64
	MeanRetriesPerRun          float64
	TotalCorrectnessSampled    int64
	TotalCorrectnessPassed     int64
	TotalCorrectnessFailed     int64
	OverallCorrectnessPassRate float64
}

func main() {
	dataDir := "data"
	if len(os.Args) > 1 && strings.TrimSpace(os.Args[1]) != "" {
		dataDir = os.Args[1]
	}

	rows, err := collectRunRows(dataDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		os.Exit(1)
	}
	if len(rows) == 0 {
		fmt.Fprintf(os.Stderr, "No summary.json files found under %s\n", dataDir)
		os.Exit(1)
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].ExperimentName == rows[j].ExperimentName {
			return rows[i].RunID < rows[j].RunID
		}
		return rows[i].ExperimentName < rows[j].ExperimentName
	})

	analysisDir := filepath.Join(dataDir, "analysis")
	if err := os.MkdirAll(analysisDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: mkdir %s: %v\n", analysisDir, err)
		os.Exit(1)
	}

	byRunPath := filepath.Join(analysisDir, "summary_by_run.csv")
	if err := writeByRunCSV(byRunPath, rows); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: write %s: %v\n", byRunPath, err)
		os.Exit(1)
	}

	agg := aggregate(rows)
	aggPath := filepath.Join(analysisDir, "summary_aggregated.csv")
	if err := writeAggregatedCSV(aggPath, agg); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: write %s: %v\n", aggPath, err)
		os.Exit(1)
	}

	fmt.Printf("Wrote %d run rows to %s\n", len(rows), byRunPath)
	fmt.Printf("Wrote %d aggregate rows to %s\n", len(agg), aggPath)
}

func collectRunRows(dataDir string) ([]runRow, error) {
	var rows []runRow
	err := filepath.WalkDir(dataDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() != "summary.json" {
			return nil
		}

		var s runSummary
		if err := readJSON(path, &s); err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}

		total := s.Completed + s.Failed
		successRate := ratio(s.Completed, total)
		correctnessRate := ratio(s.CorrectnessPassed, s.CorrectnessSampled)

		rows = append(rows, runRow{
			RunID:               s.RunID,
			ExperimentName:      s.ExperimentName,
			OperationMode:       s.OperationMode,
			Completed:           s.Completed,
			Failed:              s.Failed,
			Retries:             s.Retries,
			SuccessRate:         successRate,
			CorrectnessSampled:  s.CorrectnessSampled,
			CorrectnessPassed:   s.CorrectnessPassed,
			CorrectnessFailed:   s.CorrectnessFailed,
			CorrectnessPassRate: correctnessRate,
			RunDir:              filepath.Dir(path),
		})
		return nil
	})
	return rows, err
}

func readJSON(path string, out interface{}) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewDecoder(f).Decode(out)
}

func writeByRunCSV(path string, rows []runRow) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	defer w.Flush()

	header := []string{
		"run_id",
		"experiment_name",
		"operation_mode",
		"completed",
		"failed",
		"retries",
		"success_rate",
		"correctness_sampled",
		"correctness_passed",
		"correctness_failed",
		"correctness_pass_rate",
		"run_dir",
	}
	if err := w.Write(header); err != nil {
		return err
	}

	for _, r := range rows {
		row := []string{
			r.RunID,
			r.ExperimentName,
			r.OperationMode,
			strconv.FormatInt(r.Completed, 10),
			strconv.FormatInt(r.Failed, 10),
			strconv.FormatInt(r.Retries, 10),
			strconv.FormatFloat(r.SuccessRate, 'f', 6, 64),
			strconv.FormatInt(r.CorrectnessSampled, 10),
			strconv.FormatInt(r.CorrectnessPassed, 10),
			strconv.FormatInt(r.CorrectnessFailed, 10),
			strconv.FormatFloat(r.CorrectnessPassRate, 'f', 6, 64),
			r.RunDir,
		}
		if err := w.Write(row); err != nil {
			return err
		}
	}
	return w.Error()
}

func aggregate(rows []runRow) []aggRow {
	type acc struct {
		runs                    int64
		totalCompleted          int64
		totalFailed             int64
		totalRetries            int64
		totalCorrectnessSampled int64
		totalCorrectnessPassed  int64
		totalCorrectnessFailed  int64
		sumSuccessRate          float64
	}
	group := map[string]*acc{}

	for _, r := range rows {
		key := r.ExperimentName + "|" + r.OperationMode
		a := group[key]
		if a == nil {
			a = &acc{}
			group[key] = a
		}
		a.runs++
		a.totalCompleted += r.Completed
		a.totalFailed += r.Failed
		a.totalRetries += r.Retries
		a.totalCorrectnessSampled += r.CorrectnessSampled
		a.totalCorrectnessPassed += r.CorrectnessPassed
		a.totalCorrectnessFailed += r.CorrectnessFailed
		a.sumSuccessRate += r.SuccessRate
	}

	out := make([]aggRow, 0, len(group))
	for key, a := range group {
		parts := strings.SplitN(key, "|", 2)
		experimentName := parts[0]
		operationMode := ""
		if len(parts) > 1 {
			operationMode = parts[1]
		}

		total := a.totalCompleted + a.totalFailed
		out = append(out, aggRow{
			ExperimentName:             experimentName,
			OperationMode:              operationMode,
			Runs:                       a.runs,
			TotalCompleted:             a.totalCompleted,
			TotalFailed:                a.totalFailed,
			OverallSuccessRate:         ratio(a.totalCompleted, total),
			MeanSuccessRate:            float64Div(a.sumSuccessRate, float64(a.runs)),
			TotalRetries:               a.totalRetries,
			MeanRetriesPerRun:          float64Div(float64(a.totalRetries), float64(a.runs)),
			TotalCorrectnessSampled:    a.totalCorrectnessSampled,
			TotalCorrectnessPassed:     a.totalCorrectnessPassed,
			TotalCorrectnessFailed:     a.totalCorrectnessFailed,
			OverallCorrectnessPassRate: ratio(a.totalCorrectnessPassed, a.totalCorrectnessSampled),
		})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].ExperimentName == out[j].ExperimentName {
			return out[i].OperationMode < out[j].OperationMode
		}
		return out[i].ExperimentName < out[j].ExperimentName
	})
	return out
}

func writeAggregatedCSV(path string, rows []aggRow) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	defer w.Flush()

	header := []string{
		"experiment_name",
		"operation_mode",
		"runs",
		"total_completed",
		"total_failed",
		"overall_success_rate",
		"mean_success_rate",
		"total_retries",
		"mean_retries_per_run",
		"total_correctness_sampled",
		"total_correctness_passed",
		"total_correctness_failed",
		"overall_correctness_pass_rate",
	}
	if err := w.Write(header); err != nil {
		return err
	}

	for _, r := range rows {
		row := []string{
			r.ExperimentName,
			r.OperationMode,
			strconv.FormatInt(r.Runs, 10),
			strconv.FormatInt(r.TotalCompleted, 10),
			strconv.FormatInt(r.TotalFailed, 10),
			strconv.FormatFloat(r.OverallSuccessRate, 'f', 6, 64),
			strconv.FormatFloat(r.MeanSuccessRate, 'f', 6, 64),
			strconv.FormatInt(r.TotalRetries, 10),
			strconv.FormatFloat(r.MeanRetriesPerRun, 'f', 6, 64),
			strconv.FormatInt(r.TotalCorrectnessSampled, 10),
			strconv.FormatInt(r.TotalCorrectnessPassed, 10),
			strconv.FormatInt(r.TotalCorrectnessFailed, 10),
			strconv.FormatFloat(r.OverallCorrectnessPassRate, 'f', 6, 64),
		}
		if err := w.Write(row); err != nil {
			return err
		}
	}
	return w.Error()
}

func ratio(num, den int64) float64 {
	if den <= 0 {
		return 0
	}
	return float64(num) / float64(den)
}

func float64Div(num, den float64) float64 {
	if den == 0 {
		return 0
	}
	return num / den
}
