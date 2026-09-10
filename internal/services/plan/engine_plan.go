package plan

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/confluentinc/kcp/internal/build_info"
	"github.com/confluentinc/kcp/internal/services/plan/engine"
	"github.com/confluentinc/kcp/internal/services/report"
)

// enginePlanSchemaVersion identifies the new engine-driven plan JSON shape,
// separate from the legacy Plan's "1".
const enginePlanSchemaVersion = "2"

// AppPlan is one declared application's migration plan: the app-scoped answers
// plus a full engine verdict set. Its infra verdicts (cluster type / networking /
// auth) match the enclosing ClusterPlan by construction; only the app verdicts
// (switchover, schema, connectors, topics, historical) differ.
type AppPlan struct {
	Name            string              `json:"name"`
	Questions       []ResolvedQuestion  `json:"questions"`
	Plan            engine.PlanResult   `json:"plan"`
	Contingent      map[string][]string `json:"contingent,omitempty"` // verdict → open required questions that would change it
	ConnectorSource *ConnectorSource    `json:"connector_source,omitempty"`
}

// ConnectorSource records which source connector runtimes this plan runs, so the
// migrate-connectors step can pick the right subcommand(s): MSK Connect -> `msk`,
// self-managed Kafka Connect -> `self-managed`. A plan can run both. Nil when the
// plan runs no connectors (nothing to migrate).
type ConnectorSource struct {
	MSKConnect  bool `json:"msk_connect,omitempty"`
	SelfManaged bool `json:"self_managed,omitempty"`
}

// connectorSourceFromProfile lifts the connector-runtime flags off a resolved
// profile (scan facts or answers) for the migrate-connectors command. Returns nil
// when the plan runs no connectors, so the field omits from plan.json.
func connectorSourceFromProfile(p engine.Profile) *ConnectorSource {
	yes := func(s *string) bool { return s != nil && *s == "Yes" }
	cs := ConnectorSource{MSKConnect: yes(p.MSKConnectPresent), SelfManaged: yes(p.SelfManagedConnectors)}
	if !cs.MSKConnect && !cs.SelfManaged {
		return nil
	}
	return &cs
}

// ClusterPlan is one scanned cluster's plan: the source summary, the shared infra
// verdicts, and one migration plan per application. With no declared apps the
// single implicit app is the cluster's own Plan (Apps is empty).
type ClusterPlan struct {
	ClusterID       string              `json:"cluster_id"`
	Arn             string              `json:"arn,omitempty"`
	Key             string              `json:"key"` // plan-inputs.yaml key: name, or name@region on collision
	Region          string              `json:"region"`
	MigrationInfra  MigrationInfra      `json:"migration_infra"`
	IsServerless    bool                `json:"is_serverless"`
	TopicCount      int                 `json:"topic_count"`
	BrokerCount     int                 `json:"broker_count"`
	SourceAuths     []string            `json:"source_auths"`
	ScanFacts       []Fact              `json:"scan_facts,omitempty"`
	Questions       []ResolvedQuestion  `json:"questions"`
	Observations    []Observation       `json:"observations,omitempty"`
	Plan            engine.PlanResult   `json:"plan"`
	State           string              `json:"state"`       // canonical whole-cluster state (StateReady|StateNeedsAnswers|StateNeedsSpecialist)
	InfraState      string              `json:"infra_state"` // state of the infrastructure half (matches the plan.md summary column)
	AppState        string              `json:"app_state"`   // state of the application half
	PartitionCount  int                 `json:"partition_count"`
	Contingent      map[string][]string `json:"contingent,omitempty"`    // verdict → open required questions that would change it
	SchemaSource    *SchemaSourceRef    `json:"schema_source,omitempty"` // scan-derived source Schema Registry, for the migrate-schemas command
	ConnectorSource *ConnectorSource    `json:"connector_source,omitempty"`
	Apps            []AppPlan           `json:"apps,omitempty"`

	// effectiveSourceAuths is the source auth after answers are folded in (kcp tokens:
	// iam/scram/mtls/unauth), for render-side source-auth branching. SourceAuths is
	// scan-only and stays empty in questionnaire mode, so the renderer reads this.
	// Not serialized (unexported) — plan.json's source_auths contract is unchanged.
	effectiveSourceAuths []string
}

// SchemaSourceRef carries the source Schema Registry identifiers the
// migrate-schemas command needs. Schema registries are environment-level in the
// scan, so this is the same for every cluster; it is filled only when the scan
// found exactly one registry (otherwise the command falls back to a placeholder).
type SchemaSourceRef struct {
	GlueRegistry string `json:"glue_registry,omitempty"`
	GlueRegion   string `json:"glue_region,omitempty"`
	ConfluentURL string `json:"confluent_url,omitempty"`
}

// schemaSourceRef lifts the source Schema Registry identifiers from the scan for
// the migrate-schemas command. Confluent SR wins over Glue (matching
// detectSchemaRegistryKind). Returns nil when the scan found no registry, or more
// than one of a kind (ambiguous — the command then uses a placeholder).
func schemaSourceRef(state report.ProcessedState) *SchemaSourceRef {
	sr := state.SchemaRegistries
	if sr == nil {
		return nil
	}
	if len(sr.ConfluentSchemaRegistry) == 1 {
		return &SchemaSourceRef{ConfluentURL: sr.ConfluentSchemaRegistry[0].URL}
	}
	if len(sr.AWSGlue) == 1 {
		return &SchemaSourceRef{GlueRegistry: sr.AWSGlue[0].RegistryName, GlueRegion: sr.AWSGlue[0].Region}
	}
	return nil
}

// Canonical plan-state vocabulary, shared by plan.md (display labels) and plan.json
// (machine codes) so the two can never drift. A plan half or a whole cluster is in
// exactly one of these states.
const (
	StateReady           = "ready"            // no open required questions, not withheld
	StateNeedsAnswers    = "needs_answers"    // has open required questions
	StateNeedsSpecialist = "needs_specialist" // routed to a specialist (withheld)
)

// Canonical ResolvedQuestion.Status vocabulary. open_inert is an intake question
// that is required in the catalog but provably changes nothing for THIS cluster
// (its answer never moves the recommendation fingerprint): it stays answerable but
// does not block — it is not counted in OpenRequired and not listed as "Action
// needed".
const (
	statusOpenRequired = "open_required"
	statusOpenOptional = "open_optional"
	statusOpenInert    = "open_inert"
)

// stateLabel maps a canonical state code to its human display label (the single
// source of these strings for plan.md).
func stateLabel(code string) string {
	switch code {
	case StateReady:
		return "Ready"
	case StateNeedsAnswers:
		return "Needs answers"
	case StateNeedsSpecialist:
		return "Needs a specialist"
	}
	return code
}

// clusterState returns a cluster's canonical whole-cluster state: withheld wins,
// then any open required question, else ready. Used for both the summary counts
// and each cluster's `state` field so they can't disagree.
func clusterState(cp ClusterPlan) string {
	req, _ := openCounts(cp.Questions)
	switch {
	case cp.Plan.Withheld:
		return StateNeedsSpecialist
	case req > 0:
		return StateNeedsAnswers
	default:
		return StateReady
	}
}

// PlanSummary is the fleet-level roll-up: how many clusters are ready to act on
// vs. still need answers (open required questions) vs. need a specialist, plus the
// total open-question counts across the fleet. Field names mirror the canonical
// State* codes.
type PlanSummary struct {
	Clusters        int `json:"clusters"`
	Regions         int `json:"regions"`
	Ready           int `json:"ready"`            // StateReady
	NeedsAnswers    int `json:"needs_answers"`    // StateNeedsAnswers
	NeedsSpecialist int `json:"needs_specialist"` // StateNeedsSpecialist
	OpenRequired    int `json:"open_required"`
	OpenOptional    int `json:"open_optional"`
}

// EnginePlan is the fleet output: a header plus one ClusterPlan per scanned
// cluster. Questions are per-cluster (kcp models one plan per cluster).
type EnginePlan struct {
	Header       PlanHeader    `json:"header"`
	Summary      PlanSummary   `json:"summary"`
	Clusters     []ClusterPlan `json:"clusters"`
	TotalRegions int           `json:"total_regions"`
	Warnings     []string      `json:"warnings,omitempty"`
}

// ValidateDeclaredClusters returns one message per declared cluster key that
// matches no cluster in the scan (a misspelled cluster name). Answers under such a
// key never reach any plan, so the caller treats these as fatal input errors. It is
// validated against the full scan so an active --cluster-id/--region filter can't
// make an out-of-scope but correctly spelled key look like a typo.
func ValidateDeclaredClusters(declared DeclaredInputs, state report.ProcessedState) []string {
	keys := computeClusterKeys(collectClusters(state))
	valid := make(map[string]bool, len(keys))
	for _, k := range keys {
		valid[k] = true
	}
	declaredKeys := make([]string, 0, len(declared.Clusters))
	for k := range declared.Clusters {
		declaredKeys = append(declaredKeys, k)
	}
	sort.Strings(declaredKeys)
	var errs []string
	for _, k := range declaredKeys {
		if !valid[k] {
			errs = append(errs, fmt.Sprintf("references cluster %q, which is not in the scan", k))
		}
	}
	return errs
}

// BuildEnginePlan produces the engine-driven plan from processed state and the
// customer-declared inputs. Each cluster is mapped through buildProfile and run
// through engine.ComputePlan. Inputs are layered: fleet-wide `defaults:` are
// overlaid with each cluster's own answers (declared.For(key)).
func BuildEnginePlan(state report.ProcessedState, declared DeclaredInputs, stateFilePath string, now func() time.Time) *EnginePlan {
	if now == nil {
		now = time.Now
	}
	backfillAggregates(&state)
	clusters := collectClusters(state)
	sort.SliceStable(clusters, func(i, j int) bool {
		if clusters[i].Region != clusters[j].Region {
			return clusters[i].Region < clusters[j].Region
		}
		if clusters[i].Name != clusters[j].Name {
			return clusters[i].Name < clusters[j].Name
		}
		return clusters[i].Arn < clusters[j].Arn
	})

	ep := &EnginePlan{
		Header: PlanHeader{
			Source:            "Amazon MSK",
			StateFilePath:     stateFilePath,
			KCPVersion:        build_info.Version,
			GeneratedAt:       now().UTC(),
			StateGeneratedAt:  state.Timestamp.UTC(),
			PlanSchemaVersion: enginePlanSchemaVersion,
		},
		TotalRegions: countRegions(state),
	}
	// Cluster keys: bare name, then name@region when a name repeats, then an
	// ARN-derived suffix if name@region still collides — guaranteeing the
	// plan-inputs.yaml mapping keys stay unique (no duplicate-key file).
	keys := computeClusterKeys(clusters)

	srKind := detectSchemaRegistryKind(state)
	schemaSrc := schemaSourceRef(state) // environment-level: same for every cluster
	// No state file means a pure questionnaire: every scan-derivable fact is really
	// an answer, so provenance leads must read "your answer", not "your scan".
	scanless := stateFilePath == ""
	for i, c := range clusters {
		key := keys[i]
		in := declared.For(key)
		appNames := declared.AppNames(key)

		// With declared apps, the applications are authoritative for data movement:
		// the cluster's own inherited move_existing_data is not a separate signal.
		// Derive both the cluster-level switchover driver and the shared-infra egress
		// from the union of app needs, so the switchover, networking egress, and
		// historical-data verdicts all agree (a cluster moves data iff some app does;
		// if every app starts fresh, so does the cluster). This is also why there is
		// no "infrastructure change needed" divergence: infra is the union of app
		// needs, so an app can never need infra the cluster didn't provision.
		appInputs := make([]IntakeInputs, len(appNames))
		for i, name := range appNames {
			appInputs[i] = declared.ForApp(key, name)
		}
		anyMoves := anyAppMovesData(in, appInputs)

		profile := buildProfile(c, in, srKind, scanless)
		if len(appNames) > 0 {
			profile.NeedsDataMigration = anyMoves
			profile.AnyAppNeedsDataMigration = anyMoves
		}

		// Precedence levels for attributing an answer's source (highest first).
		clusterOwn := declared.Clusters[key].Inputs
		clusterLevels := []answerLevel{{sourceCluster, clusterOwn}, {sourceAllClusters, declared.Defaults}}

		clusterQuestions := resolveQuestions(profile, in)
		annotateAnswerSource(clusterQuestions, clusterLevels, scanless)

		// With no scan, the "scan facts" are really answers/presets (see
		// annotateAnswerSource), so don't publish a scan_facts block that would claim a
		// scan the run never had.
		scanFacts := scanFactsOf(profile)
		if scanless {
			scanFacts = nil
		}

		clusterPlanResult := engine.ComputePlan(profile)
		clusterContingent, clusterInert := computeContingent(c, in, srKind, scanless, clusterPlanResult, clusterQuestions)
		downgradeInert(clusterQuestions, clusterInert)
		cp := ClusterPlan{
			ClusterID:      c.Name,
			Arn:            c.Arn,
			Key:            key,
			Region:         c.Region,
			MigrationInfra: migrationInfraFor(profile, clusterPlanResult),
			// Reflect the effective profile, not the raw scan: a source_cluster_type
			// override (e.g. no-scan mode, or correcting a mis-scan) can make the plan
			// serverless, and this flag must agree with the switchover path it drives.
			IsServerless:    engine.IsServerless(profile),
			TopicCount:      topicCount(c),
			BrokerCount:     brokerCount(c),
			PartitionCount:  userPartitionsOf(c),
			SourceAuths:     sourceAuthsDetected(c),
			ScanFacts:       scanFacts,
			Questions:       clusterQuestions,
			Observations:    clusterObservations(c, profile),
			Plan:            clusterPlanResult,
			Contingent:      clusterContingent,
			SchemaSource:    schemaSrc,
			ConnectorSource: connectorSourceFromProfile(profile),
		}
		cp.effectiveSourceAuths = engineAuthToKCP(profile.SourceAuthTypes)
		cp.State = clusterState(cp)
		// Per-half states, mirroring the plan.md summary columns so a consumer can
		// reproduce the Infrastructure/Application badges from plan.json.
		cp.InfraState = planGroupStateCode(cp.Plan.Withheld, cp.Contingent, infraVerdicts)
		cp.AppState = planGroupStateCode(cp.Plan.Withheld, cp.Contingent, appVerdicts)
		// A withheld cluster shows no verdicts (plan.md and plan.json both redact
		// them), so its contingent map, which references those hidden verdicts, is
		// dropped too.
		if cp.Plan.Withheld {
			cp.Contingent = nil
		}
		for i, name := range appNames {
			ap := buildProfile(c, appInputs[i], srKind, scanless)
			ap.AnyAppNeedsDataMigration = anyMoves
			appQs := appScopedQuestions(resolveQuestions(ap, appInputs[i]))
			appOwn := declared.Clusters[key].Apps[name]
			annotateAnswerSource(appQs, []answerLevel{{sourceApp, appOwn}, {sourceCluster, clusterOwn}, {sourceAllClusters, declared.Defaults}}, scanless)
			appPlan := engine.ComputePlan(ap)
			appContingent, appInert := computeContingent(c, appInputs[i], srKind, scanless, appPlan, appQs)
			downgradeInert(appQs, appInert)
			if appPlan.Withheld {
				appContingent = nil
			}
			cp.Apps = append(cp.Apps, AppPlan{
				Name:            name,
				Questions:       appQs,
				Plan:            appPlan,
				Contingent:      appContingent,
				ConnectorSource: connectorSourceFromProfile(ap),
			})
		}
		ep.Clusters = append(ep.Clusters, cp)
	}
	// report plan is MSK-only today; flag any Apache Kafka (OSK) clusters it skips
	// rather than silently dropping them (they'd otherwise vanish with no trace).
	if n := oskClusterCount(state); n > 0 {
		ep.Warnings = append(ep.Warnings, fmt.Sprintf("report plan does not yet support Apache Kafka sources; %d cluster(s) skipped", n))
	}
	ep.Summary = summarize(ep)
	return ep
}

// summarize rolls the per-cluster plans up into the fleet summary.
func summarize(ep *EnginePlan) PlanSummary {
	s := PlanSummary{Clusters: len(ep.Clusters), Regions: ep.TotalRegions}
	for _, cp := range ep.Clusters {
		switch clusterState(cp) {
		case StateNeedsSpecialist:
			s.NeedsSpecialist++
			// A specialist-routed cluster's questions are context for that
			// conversation, not blockers to an automated plan, so they are not
			// counted in the fleet open-question totals.
			continue
		case StateNeedsAnswers:
			s.NeedsAnswers++
		default:
			s.Ready++
		}
		req, opt := openCounts(cp.Questions)
		s.OpenRequired += req
		s.OpenOptional += opt
	}
	return s
}

// computeClusterKeys assigns each cluster a plan-inputs.yaml key that is unique
// across the fleet: the bare name, or name@region when the name repeats, or
// name@region@<arn-tail> when even that collides (same name in the same region).
func computeClusterKeys(clusters []report.ProcessedCluster) []string {
	nameCount := map[string]int{}
	for _, c := range clusters {
		nameCount[c.Name]++
	}
	tentative := make([]string, len(clusters))
	tentCount := map[string]int{}
	for i, c := range clusters {
		k := c.Name
		if nameCount[c.Name] > 1 {
			k = c.Name + "@" + c.Region
		}
		tentative[i] = k
		tentCount[k]++
	}
	keys := make([]string, len(clusters))
	for i, c := range clusters {
		if tentCount[tentative[i]] > 1 {
			suffix := arnTail(c.Arn)
			if suffix == "" {
				suffix = itoa(i)
			}
			keys[i] = tentative[i] + "@" + suffix
		} else {
			keys[i] = tentative[i]
		}
	}
	return keys
}

// arnTail returns the last "/"-separated segment of an ARN (the unique cluster
// UUID for MSK), or "" when there is none.
func arnTail(arn string) string {
	if i := strings.LastIndex(arn, "/"); i >= 0 && i+1 < len(arn) {
		return arn[i+1:]
	}
	return ""
}

// Canonical answer-source codes: snake_case machine values on
// ResolvedQuestion.Source, serialized to plan.json and mapped to a human phrase for
// plan.md by sourceLabel — one vocabulary, two renderings.
const (
	sourceApp            = "app"
	sourceCluster        = "cluster"
	sourceAllClusters    = "all_clusters"
	sourceBuiltInDefault = "built_in_default"
	sourceScan           = "scan"
	sourceOverride       = "override"
)

// answerLevel names one precedence level and the raw (un-merged) inputs declared
// at it, for attributing where an effective answer came from.
type answerLevel struct {
	name string
	in   IntakeInputs
}

// annotateAnswerSource fills each declared question's Source with the highest-
// precedence level that actually set it (levels are given highest-first). A
// question with a value that no level set is a built-in default; a scan question
// is sourced from the scan.
func annotateAnswerSource(qs []ResolvedQuestion, levels []answerLevel, scanless bool) {
	byKey := map[string]question{}
	for _, q := range catalog {
		byKey[q.Key] = q
	}
	for i := range qs {
		if qs[i].Scan {
			// With a scan, a scan-category fact is sourced from the scan (or an override
			// of it). With no scan, its value is really an answer/preset — so don't claim
			// "scan"/"override": fall through to answer-level attribution, and don't leave
			// the question flagged with the "scan" status.
			if !scanless {
				if qs[i].Overridden {
					qs[i].Source = sourceOverride
				} else {
					qs[i].Source = sourceScan
				}
				continue
			}
			if qs[i].Status == "scan" {
				qs[i].Status = "answered"
			}
		}
		q, ok := byKey[qs[i].Key]
		if !ok {
			continue
		}
		for _, lv := range levels {
			if len(q.engineValues(lv.in)) > 0 {
				qs[i].Source = lv.name
				break
			}
		}
		if qs[i].Source == "" && qs[i].Value != "" {
			qs[i].Source = sourceBuiltInDefault
		}
	}
}

// Canonical plan-node IDs: the stable internal keys for the nine recommendation
// nodes. Contingent maps, the infra/app verdict groups, observation routing, and
// every isContingent lookup key on these, decoupled from the display copy so
// renaming a user-facing label can never silently break contingency or
// observation routing. Display labels come from nodeLabel; plan.json keeps the
// label as its key (mapped at the serialization boundary), so the JSON contract
// is unchanged.
const (
	nodeClusterType   = "cluster_type"
	nodeSizing        = "sizing"
	nodeNetworking    = "networking"
	nodeAuth          = "auth"
	nodeDataMigration = "data_migration"
	nodeSchema        = "schema"
	nodeConnectors    = "connectors"
	nodeTopics        = "topics"
	nodeHistorical    = "historical_data"
)

// nodeLabels is the single id→display-label lookup, used both for the plan.md
// text (table row title, "Why" bullet heading) and for the plan.json contingent
// keys, so the machine record and the display copy derive from one place.
var nodeLabels = map[string]string{
	nodeClusterType:   "Cluster type",
	nodeSizing:        "Sizing",
	nodeNetworking:    "Networking",
	nodeAuth:          "Authentication",
	nodeDataMigration: "Data migration",
	nodeSchema:        "Schema",
	nodeConnectors:    "Connectors",
	nodeTopics:        "Topics",
	nodeHistorical:    "Historical data",
}

// nodeLabel maps a plan-node ID to its human display label, falling back to the
// ID for any unmapped key.
func nodeLabel(id string) string {
	if l, ok := nodeLabels[id]; ok {
		return l
	}
	return id
}

// labelContingent re-keys an ID-keyed contingent map to display-label keys, for
// the plan.json serialization boundary so the JSON contract stays exactly as it
// was (label keys) while the in-memory wiring is keyed by stable node IDs.
func labelContingent(c map[string][]string) map[string][]string {
	if c == nil {
		return nil
	}
	out := make(map[string][]string, len(c))
	for id, keys := range c {
		out[nodeLabel(id)] = keys
	}
	return out
}

// MarshalJSON keeps plan.json's `contingent` keyed by display labels (the
// existing JSON contract) even though the in-memory map is keyed by node IDs, and
// stamps `pending: true` (blanking the concrete value) on every verdict still held
// on an unanswered question, so plan.json represents "pending" uniformly — the same
// gate plan.md reads off the contingent map — instead of leaking a provisional
// recommendation. a is a marshaling copy, so the source ClusterPlan is untouched.
func (cp ClusterPlan) MarshalJSON() ([]byte, error) {
	type alias ClusterPlan
	a := alias(cp)
	a.Contingent = labelContingent(cp.Contingent)
	applyPendingVerdicts(&a.Plan, cp.Contingent)
	applyPendingMigrationInfra(&a.MigrationInfra, cp.Contingent)
	return json.Marshal(a)
}

// MarshalJSON keeps plan.json's `contingent` keyed by display labels (the
// existing JSON contract) even though the in-memory map is keyed by node IDs, and
// stamps `pending: true` on this application's still-held verdicts (see
// ClusterPlan.MarshalJSON).
func (ap AppPlan) MarshalJSON() ([]byte, error) {
	type alias AppPlan
	a := alias(ap)
	a.Contingent = labelContingent(ap.Contingent)
	applyPendingVerdicts(&a.Plan, ap.Contingent)
	return json.Marshal(a)
}

// applyPendingVerdicts stamps pending:true and blanks the concrete value on every
// plan verdict whose node still has unanswered deciding questions in contingent
// (keyed by the in-memory node IDs). This makes plan.json represent "pending" the
// one way plan.md does — a held row — rather than the three inconsistent encodings
// it used to leak (concrete values, held:true, and a "Not assessed" sentinel). A
// withheld plan already redacts its verdicts (PlanResult.MarshalJSON), and a Ready
// cluster has no contingent nodes, so both are left exactly as they were.
func applyPendingVerdicts(plan *engine.PlanResult, contingent map[string][]string) {
	if plan.Withheld {
		return
	}
	for node := range contingent {
		switch node {
		case nodeClusterType:
			plan.ClusterType.Pending, plan.ClusterType.Value = true, ""
			// Blank the descriptive siblings too, so a pending verdict withholds the
			// provisional recommendation in plan.json exactly as plan.md does — not just
			// the value while reason/why/action/tier still leak the held pick.
			plan.ClusterType.Reason, plan.ClusterType.Why, plan.ClusterType.Action = "", "", ""
			plan.ClusterType.Tier, plan.ClusterType.AssessmentReason = "", ""
			plan.ClusterType.ZoneConfig, plan.ClusterType.ZoneReason = "", ""
		case nodeSizing:
			plan.Sizing.Pending, plan.Sizing.Value = true, ""
		case nodeNetworking:
			plan.Networking.Pending, plan.Networking.Value = true, ""
			// Same as cluster type: withhold the provisional networking recommendation's
			// descriptive siblings, not just its value.
			plan.Networking.Reason, plan.Networking.Why, plan.Networking.How, plan.Networking.Fit = "", "", "", ""
			plan.Networking.Action = nil
			plan.Networking.Pros, plan.Networking.Cons = nil, nil
		case nodeAuth:
			plan.Auth.Pending, plan.Auth.Value = true, ""
		case nodeDataMigration:
			plan.Switchover.Pending, plan.Switchover.Value = true, ""
		case nodeSchema:
			plan.Schema.Pending, plan.Schema.Value = true, ""
		case nodeConnectors:
			plan.Connectors.Pending, plan.Connectors.Value = true, ""
		case nodeTopics:
			plan.Topics.Pending, plan.Topics.Value = true, ""
		case nodeHistorical:
			plan.HistoricalData.Pending, plan.HistoricalData.Value = true, ""
		}
	}
}

// applyPendingMigrationInfra blanks the migration-infra verdict to pending:true when
// the infrastructure or data-movement verdicts it is derived from are still held, so
// plan.json doesn't publish a provisional topology (mirroring plan.md, which holds
// the whole migration-steps section until those verdicts settle). mi is a marshaling
// copy. Contingent is nil for a withheld cluster, so its "Determined with a
// specialist" infra is left as-is.
func applyPendingMigrationInfra(mi *MigrationInfra, contingent map[string][]string) {
	for _, node := range []string{nodeClusterType, nodeNetworking, nodeAuth, nodeDataMigration} {
		if len(contingent[node]) > 0 {
			*mi = MigrationInfra{Pending: true, CCType: mi.CCType}
			return
		}
	}
}

// verdictValues flattens a plan's customer-facing verdicts to node-id→value, for
// comparing whether a verdict is stable across possible answers.
func verdictValues(p engine.PlanResult) map[string]string {
	return map[string]string{
		nodeClusterType:   string(p.ClusterType.Value),
		nodeSizing:        p.Sizing.Value,
		nodeNetworking:    p.Networking.Value,
		nodeAuth:          p.Auth.Value,
		nodeDataMigration: p.Switchover.Value,
		nodeSchema:        p.Schema.Value,
		nodeConnectors:    p.Connectors.Value,
		nodeTopics:        p.Topics.Value,
		nodeHistorical:    p.HistoricalData.Value,
	}
}

// recFingerprint captures the full recommendation surface a question could move —
// not just the nine verdict values, but also whether the plan is withheld, the set
// of human-assist trigger IDs, and whether networking offers an alternative. Two
// plans with the same fingerprint recommend the same thing; a question whose
// fingerprint never moves across its options changes nothing for that cluster and is
// inert there. This is deliberately wider than verdictValues: use_case_breadth and
// connects_today can flip Withheld or the networking Alternative without moving any
// verdict Value, and a value-only check would wrongly treat them as inert and hide
// them.
func recFingerprint(p engine.PlanResult) string {
	var b strings.Builder
	vv := verdictValues(p)
	names := make([]string, 0, len(vv))
	for n := range vv {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		b.WriteString(n)
		b.WriteByte('=')
		b.WriteString(vv[n])
		b.WriteByte(';')
	}
	fmt.Fprintf(&b, "withheld=%t;", p.Withheld)
	ids := make([]string, 0, len(p.HumanAssist.Triggers))
	for _, t := range p.HumanAssist.Triggers {
		ids = append(ids, t.ID)
	}
	sort.Strings(ids)
	b.WriteString("triggers=")
	b.WriteString(strings.Join(ids, ","))
	fmt.Fprintf(&b, ";netalt=%t", p.Networking.Alternative != nil)
	return b.String()
}

// computeContingent finds, for the given profile, which verdicts would change if
// the still-open required questions were answered differently, and — in the same
// perturbation sweep — which of those questions are inert for this cluster. It
// perturbs each open required question through each of its options (others held at
// their current/default value) and records every verdict whose value moves (the
// contingent map) and whether the full recommendation fingerprint ever moves (if it
// never does, the question is inert here). A verdict that never moves is determined
// by the scan and safe to recommend; one that does is held until the deciding
// question(s) are answered. Marginal (one question at a time), which catches the
// dominant cases (e.g. networking → cluster type).
//
// The contingent map is keyed on verdict Values only (its consumers gate on verdict
// rows), and is unchanged by this pass — an inert question moves no verdict Value,
// so it never contributed to it. The inert set is keyed on the wider recommendation
// fingerprint, so a question that flips only Withheld or the networking Alternative
// is (correctly) NOT inert.
func computeContingent(c report.ProcessedCluster, in IntakeInputs, srKind string, scanless bool, baseline engine.PlanResult, questions []ResolvedQuestion) (map[string][]string, map[string]bool) {
	base := verdictValues(baseline)
	baseFP := recFingerprint(baseline)
	byKey := map[string]question{}
	for _, q := range allQuestions() {
		byKey[q.Key] = q
	}
	deciders := map[string]map[string]bool{} // verdict → set of deciding question keys
	inert := map[string]bool{}               // question keys whose fingerprint never moves here
	for _, rq := range questions {
		if rq.Status != statusOpenRequired {
			continue
		}
		q, ok := byKey[rq.Key]
		if !ok {
			continue
		}
		moved := false // did the recommendation fingerprint move across any option?
		for _, o := range q.Opts {
			trial := in
			q.set(&trial, []string{o.Engine})
			pr := engine.ComputePlan(buildProfile(c, trial, srKind, scanless))
			v := verdictValues(pr)
			for name, val := range v {
				if val != base[name] {
					if deciders[name] == nil {
						deciders[name] = map[string]bool{}
					}
					deciders[name][rq.Key] = true
				}
			}
			if recFingerprint(pr) != baseFP {
				moved = true
			}
		}
		// A question whose fingerprint never moves across its options changes nothing
		// for this cluster and is inert. requiredWhenMissing scan facts are exempt: a
		// scan-derivable input the scan didn't capture is genuinely needed even when a
		// missing dependent answer leaves it momentarily unconsumed — only INTAKE
		// questions are downgraded when provably inert.
		if !moved && !q.requiredWhenMissing {
			inert[rq.Key] = true
		}
	}
	var out map[string][]string
	if len(deciders) > 0 {
		out = map[string][]string{}
		for name, set := range deciders {
			keys := make([]string, 0, len(set))
			for k := range set {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			out[name] = keys
		}
	}
	return out, inert
}

// downgradeInert reclassifies each open_required question flagged inert for this
// cluster to open_inert: it stays a valid, answerable question but no longer blocks
// — dropped from OpenRequired, from the "Action needed" callout, and from the
// blocking REQUIRED section of plan-inputs. Required is cleared so the plan-inputs
// grouping treats it as optional. The verdicts and the contingent map are untouched.
func downgradeInert(qs []ResolvedQuestion, inert map[string]bool) {
	if len(inert) == 0 {
		return
	}
	for i := range qs {
		if qs[i].Status == statusOpenRequired && inert[qs[i].Key] {
			qs[i].Status = statusOpenInert
			qs[i].Required = false
		}
	}
}

// anyAppMovesData returns "Yes" when any application (or the cluster itself, when
// no apps are declared) will move existing data over the migration link. An unset
// answer defaults to "needs data", so only an explicit "No" everywhere yields "No".
func anyAppMovesData(cluster IntakeInputs, apps []IntakeInputs) string {
	if len(apps) == 0 {
		if cluster.NeedsDataMigration == "No" {
			return "No"
		}
		return "Yes"
	}
	for _, in := range apps {
		if in.NeedsDataMigration != "No" {
			return "Yes"
		}
	}
	return "No"
}

// appScopedQuestions keeps only the application-scoped questions (switchover,
// schema, connectors, topics, historical) for per-app display.
func appScopedQuestions(qs []ResolvedQuestion) []ResolvedQuestion {
	var out []ResolvedQuestion
	for _, q := range qs {
		if appQuestionKeys[q.Key] {
			out = append(out, q)
		}
	}
	return out
}

// RenderEnginePlanJSON marshals the engine plan with stable, indented JSON.
func RenderEnginePlanJSON(ep *EnginePlan) (string, error) {
	b, err := json.MarshalIndent(ep, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}
