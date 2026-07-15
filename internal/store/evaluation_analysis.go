package store

import (
	"math"
	"sort"

	"github.com/crestenstclair/crest-spec/internal/db"
)

// Evaluation analysis is intentionally pure: persistence loads observations
// and stores these projections, while this file owns aggregation and winner
// policy without performing database work.
func aggregateEvaluationMetrics(run *EvaluationRun, rows []db.ListEvaluationMetricObservationsByRunRow) []EvaluationMetricAggregate {
	type key struct{ variant, split, metric string }
	values := make(map[key][]float64)
	expected := make(map[key]int)
	splits := []string{"all", "training", "development", "held_out"}
	for _, assignment := range run.Assignments {
		for _, metric := range run.MetricPolicy.Metrics {
			expected[key{assignment.VariantName, "all", metric.Name}]++
			expected[key{assignment.VariantName, assignment.Split, metric.Name}]++
		}
	}
	for _, row := range rows {
		for _, split := range []string{"all", row.Split} {
			item := key{row.VariantName, split, row.MetricName}
			if row.Value != nil {
				values[item] = append(values[item], *row.Value)
			}
		}
	}
	var result []EvaluationMetricAggregate
	for _, variant := range run.Variants {
		for _, split := range splits {
			for _, metric := range run.MetricPolicy.Metrics {
				item := key{variant.Name, split, metric.Name}
				if expected[item] == 0 {
					continue
				}
				aggregate := EvaluationMetricAggregate{
					VariantName: variant.Name, Split: split, MetricName: metric.Name,
					SampleCount: len(values[item]), MissingCount: expected[item] - len(values[item]),
				}
				if len(values[item]) > 0 {
					mean, minimum, maximum := summarizeValues(values[item])
					aggregate.Mean, aggregate.Minimum, aggregate.Maximum = &mean, &minimum, &maximum
				}
				result = append(result, aggregate)
			}
		}
	}
	return result
}

func summarizeValues(values []float64) (mean, minimum, maximum float64) {
	minimum, maximum = values[0], values[0]
	for _, value := range values {
		mean += value
		minimum = math.Min(minimum, value)
		maximum = math.Max(maximum, value)
	}
	return mean / float64(len(values)), minimum, maximum
}

func compareEvaluationMetrics(run *EvaluationRun, aggregates []EvaluationMetricAggregate) ([]EvaluationComparison, string, string, string) {
	byKey := make(map[string]EvaluationMetricAggregate)
	baseline := ""
	for _, variant := range run.Variants {
		if variant.Baseline {
			baseline = variant.Name
		}
	}
	for _, aggregate := range aggregates {
		byKey[aggregate.VariantName+"\x00"+aggregate.Split+"\x00"+aggregate.MetricName] = aggregate
	}
	metricByName := make(map[string]EvaluationMetricDefinition)
	for _, metric := range run.MetricPolicy.Metrics {
		metricByName[metric.Name] = metric
	}
	var comparisons []EvaluationComparison
	qualified := make(map[string]bool)
	for _, candidate := range run.Variants {
		if candidate.Baseline {
			continue
		}
		candidateQualified, primaryBetter := true, false
		for _, split := range []string{"all", "training", "development", "held_out"} {
			for _, metric := range run.MetricPolicy.Metrics {
				base := byKey[baseline+"\x00"+split+"\x00"+metric.Name]
				cand := byKey[candidate.Name+"\x00"+split+"\x00"+metric.Name]
				if base.SampleCount == 0 && cand.SampleCount == 0 && base.MissingCount == 0 && cand.MissingCount == 0 {
					continue
				}
				comparison := compareMetric(run, baseline, candidate.Name, split, metric, base, cand)
				comparisons = append(comparisons, comparison)
				qualifyingSplit := split == "all" || (split == "held_out" && run.RequireHeldOut)
				if metric.Primary && qualifyingSplit {
					if comparison.Conclusion == "inconclusive" || comparison.Regression {
						candidateQualified = false
					}
					if comparison.Conclusion == "candidate_better" {
						primaryBetter = true
					}
				}
			}
		}
		qualified[candidate.Name] = candidateQualified && primaryBetter
	}
	if !allEvaluationAssignmentsTerminal(run.Assignments) {
		return comparisons, "inconclusive", "", "one or more assignments are incomplete"
	}
	var winners []string
	for candidate, wins := range qualified {
		if wins {
			winners = append(winners, candidate)
		}
	}
	sort.Strings(winners)
	if len(winners) == 1 {
		return comparisons, "candidate_wins", winners[0], "candidate improves a primary metric without a primary regression"
	}
	if len(winners) > 1 {
		return comparisons, "inconclusive", "", "multiple candidates qualify; no unique winner"
	}
	for _, comparison := range comparisons {
		if comparison.Split == "all" && comparison.Regression && metricByName[comparison.MetricName].Primary {
			return comparisons, "baseline_wins", baseline, "candidate regresses a primary metric"
		}
		if comparison.Conclusion == "inconclusive" && metricByName[comparison.MetricName].Primary {
			return comparisons, "inconclusive", "", "primary metrics are incomplete or underpowered"
		}
	}
	return comparisons, "no_material_change", "", "no candidate clears the practical significance threshold"
}

func compareMetric(run *EvaluationRun, baseline, candidate, split string, metric EvaluationMetricDefinition, base, candidateAggregate EvaluationMetricAggregate) EvaluationComparison {
	threshold := metric.PracticalThreshold
	comparison := EvaluationComparison{
		BaselineVariant: baseline, CandidateVariant: candidate, Split: split, MetricName: metric.Name,
		BaselineSampleCount: base.SampleCount, CandidateSampleCount: candidateAggregate.SampleCount,
		MissingCount: base.MissingCount + candidateAggregate.MissingCount, BaselineValue: base.Mean,
		CandidateValue: candidateAggregate.Mean, PracticalThreshold: threshold, Conclusion: "inconclusive",
	}
	if base.Mean == nil || candidateAggregate.Mean == nil || base.SampleCount < run.MinimumSampleSize || candidateAggregate.SampleCount < run.MinimumSampleSize || base.SampleCount != candidateAggregate.SampleCount {
		comparison.Reason = "missing, unequal, or insufficient samples"
		return comparison
	}
	absolute := *candidateAggregate.Mean - *base.Mean
	comparison.AbsoluteChange = &absolute
	if *base.Mean != 0 {
		relative := absolute / math.Abs(*base.Mean)
		comparison.RelativeChange = &relative
	}
	improvement := absolute
	if metric.Direction == "lower" {
		improvement = -absolute
	}
	switch {
	case improvement > threshold:
		comparison.Conclusion = "candidate_better"
		comparison.Reason = "candidate improvement exceeds practical threshold"
	case improvement < -threshold:
		comparison.Conclusion = "baseline_better"
		comparison.Regression = true
		comparison.Reason = "candidate regression exceeds practical threshold"
	default:
		comparison.Conclusion = "no_material_change"
		comparison.Reason = "change is within practical threshold"
	}
	return comparison
}

func allEvaluationAssignmentsTerminal(assignments []EvaluationAssignment) bool {
	for _, assignment := range assignments {
		if assignment.Status != "submitted" && assignment.Status != "cancelled" {
			return false
		}
	}
	return true
}
