package arango

import (
	"sort"
	"strings"
)

// ExplainAssessment is a deterministic compiler-facing summary of one Arango
// explain response. Plan 0 is the primary plan when Result.Plan is present;
// alternative plans follow in response order.
type ExplainAssessment struct {
	Plans                 []ExplainPlanEstimate
	FullCollectionScans   []ExplainCollectionScan
	Indexes               []ExplainIndexSummary
	Warnings              []ExplainWarning
	AppliedOptimizerRules []string
}

type ExplainPlanEstimate struct {
	Plan             int
	EstimatedCost    float64
	EstimatedNrItems float64
}

// ExplainCollectionScan records an EnumerateCollectionNode. Such a node is a
// full collection enumeration indicator even when a different node elsewhere
// in the plan uses an index.
type ExplainCollectionScan struct {
	Plan       int
	NodeID     int64
	Collection string
}

// ExplainIndexSummary groups equivalent indexes across plan nodes while
// retaining every plan/node use.
type ExplainIndexSummary struct {
	Collection string
	ID         string
	Name       string
	Type       string
	Fields     []string
	Uses       []ExplainIndexLocation
}

type ExplainIndexLocation struct {
	Plan   int
	NodeID int64
}

// AssessExplainResult converts parsed explain data into stable findings without
// issuing queries or depending on an Arango client.
func AssessExplainResult(result ExplainResult) ExplainAssessment {
	plans := explainPlans(result)
	assessment := ExplainAssessment{
		Plans:                 make([]ExplainPlanEstimate, 0, len(plans)),
		FullCollectionScans:   []ExplainCollectionScan{},
		Indexes:               []ExplainIndexSummary{},
		Warnings:              append([]ExplainWarning{}, result.Warnings...),
		AppliedOptimizerRules: []string{},
	}

	rules := map[string]bool{}
	indexByKey := map[string]int{}
	for planNumber, plan := range plans {
		assessment.Plans = append(assessment.Plans, ExplainPlanEstimate{
			Plan: planNumber, EstimatedCost: plan.EstimatedCost, EstimatedNrItems: plan.EstimatedNrItems,
		})
		for _, rule := range plan.Rules {
			if rule = strings.TrimSpace(rule); rule != "" {
				rules[rule] = true
			}
		}
		for _, node := range plan.Nodes {
			if node.Type == "EnumerateCollectionNode" {
				assessment.FullCollectionScans = append(assessment.FullCollectionScans, ExplainCollectionScan{
					Plan: planNumber, NodeID: node.ID, Collection: node.Collection,
				})
			}
			for _, index := range node.Indexes {
				collection := explainIndexCollection(node, index)
				fields := append([]string(nil), index.Fields...)
				key := strings.Join([]string{collection, index.ID, index.Name, index.Type, strings.Join(fields, "\x1f")}, "\x00")
				position, ok := indexByKey[key]
				if !ok {
					position = len(assessment.Indexes)
					indexByKey[key] = position
					assessment.Indexes = append(assessment.Indexes, ExplainIndexSummary{
						Collection: collection, ID: index.ID, Name: index.Name, Type: index.Type, Fields: fields,
					})
				}
				assessment.Indexes[position].Uses = append(assessment.Indexes[position].Uses, ExplainIndexLocation{Plan: planNumber, NodeID: node.ID})
			}
		}
	}

	for rule := range rules {
		assessment.AppliedOptimizerRules = append(assessment.AppliedOptimizerRules, rule)
	}
	sort.Strings(assessment.AppliedOptimizerRules)
	sort.Slice(assessment.FullCollectionScans, func(i, j int) bool {
		a, b := assessment.FullCollectionScans[i], assessment.FullCollectionScans[j]
		if a.Collection != b.Collection {
			return a.Collection < b.Collection
		}
		if a.Plan != b.Plan {
			return a.Plan < b.Plan
		}
		return a.NodeID < b.NodeID
	})
	sort.Slice(assessment.Indexes, func(i, j int) bool {
		a, b := assessment.Indexes[i], assessment.Indexes[j]
		if a.Collection != b.Collection {
			return a.Collection < b.Collection
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		return a.Type < b.Type
	})
	for i := range assessment.Indexes {
		sort.Slice(assessment.Indexes[i].Uses, func(a, b int) bool {
			left, right := assessment.Indexes[i].Uses[a], assessment.Indexes[i].Uses[b]
			if left.Plan != right.Plan {
				return left.Plan < right.Plan
			}
			return left.NodeID < right.NodeID
		})
	}
	sort.Slice(assessment.Warnings, func(i, j int) bool {
		if assessment.Warnings[i].Code != assessment.Warnings[j].Code {
			return assessment.Warnings[i].Code < assessment.Warnings[j].Code
		}
		return assessment.Warnings[i].Message < assessment.Warnings[j].Message
	})
	return assessment
}

func explainPlans(result ExplainResult) []ExplainPlan {
	plans := make([]ExplainPlan, 0, 1+len(result.Plans))
	if result.Plan != nil {
		plans = append(plans, *result.Plan)
	}
	return append(plans, result.Plans...)
}
