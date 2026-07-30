package apply

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path"
	"sort"

	"github.com/IBM/sarama"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsiam "github.com/aws/aws-sdk-go-v2/service/iam"
	awsiamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/manifest"
	migrate "github.com/confluentinc/kcp/internal/migrate"
	macls "github.com/confluentinc/kcp/internal/migrate/acls"
	mclusterlink "github.com/confluentinc/kcp/internal/migrate/clusterlink"
	mmirror "github.com/confluentinc/kcp/internal/migrate/mirrortopics"
	mnew "github.com/confluentinc/kcp/internal/migrate/newtopics"
	msa "github.com/confluentinc/kcp/internal/migrate/serviceaccounts"
	"github.com/confluentinc/kcp/internal/services/clusterlink"
	iamservice "github.com/confluentinc/kcp/internal/services/iam"
	"github.com/confluentinc/kcp/internal/services/reconcile"
	"github.com/confluentinc/kcp/internal/targets"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
)

// newSourceReader builds the live source reader. It is a package-level var so
// tests can substitute a fake without opening a live Kafka connection.
var newSourceReader = func(conn types.KafkaSourceConn) migrate.Source {
	return migrate.NewKafkaSourceReader(conn)
}

// sourceACLLister is the minimal source-admin surface the ACL read needs. It
// matches both client.KafkaAdmin and a test fake.
type sourceACLLister interface {
	ListAcls() ([]sarama.ResourceAcls, error)
	Close() error
}

// newSourceACLLister opens the source Kafka admin used to read native ACLs. It
// is a package-level var so tests can substitute a fake without opening a live
// Kafka connection.
var newSourceACLLister = func(conn types.KafkaSourceConn) (sourceACLLister, error) {
	return migrate.BuildSourceAdmin(conn)
}

// newIAMFetcher builds the AWS IAM client and returns it wrapped as a
// macls.PrincipalPolicyFetcher. It is a package-level var so tests can
// substitute a fake without an AWS credential chain — mirrors
// newSourceACLLister above.
var newIAMFetcher = func() (macls.PrincipalPolicyFetcher, error) {
	iamClient, err := client.NewIAMClient()
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, principalArn string) (*iamservice.PrincipalPolicies, error) {
		return iamservice.GetPrincipalPolicies(ctx, iamClient, principalArn)
	}, nil
}

// newRolePolicyEnumerator builds the AWS IAM client and returns it wrapped
// as a macls.RolePolicyEnumerator over iamservice.GetAllRolePolicies. It is
// a package-level var — mirroring newIAMFetcher above — so tests can
// substitute a fake without an AWS credential chain.
var newRolePolicyEnumerator = func() (macls.RolePolicyEnumerator, error) {
	iamClient, err := client.NewIAMClient()
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context) ([]iamservice.PrincipalPolicies, error) {
		return iamservice.GetAllRolePolicies(ctx, iamClient)
	}, nil
}

// newEffectiveAccessChecker builds the AWS IAM client and returns it
// wrapped as a macls.EffectiveAccessChecker over iam:SimulatePrincipalPolicy.
// It is a package-level var — mirroring newIAMFetcher above — so tests can
// substitute a fake without an AWS credential chain.
//
// One SimulatePrincipalPolicy call batches ALL of a principal's distinct
// actions x resources (ActionNames/ResourceArns), matching how
// macls.FilterEffective invokes this seam. AWS returns a per-(action,
// resource) decision in EvaluationResults[].ResourceSpecificResults
// whenever more than one concrete resource ARN is simulated — the
// top-level EvaluationResult.EvalResourceName/EvalDecision fields are only
// meaningful for a "*"/single-resource simulation — so both are read here;
// whichever the SDK actually populated wins. Results are keyed
// action+"|"+resource to match macls.EffectiveAccessChecker's contract.
// Pagination (IsTruncated/Marker) is followed to completion before
// returning.
//
// SimulatePrincipalPolicy with only PolicySourceArn/ActionNames/ResourceArns
// evaluates ONLY the principal's identity-based policies — it silently
// ignores an attached permissions boundary unless the boundary document is
// also passed via PermissionsBoundaryPolicyInputList. principalArn's boundary
// (if any) is fetched and passed here — for BOTH role and user principals —
// so a boundary-capped grant is correctly reported as denied instead of
// allowed. Without this, verifyEffectiveAccess would silently miss it.
//
// One caveat remains, deliberately out of scope: service control policies
// (SCPs) are never evaluated by SimulatePrincipalPolicy at all, for either
// principal type — there is no API input for them. The "verified" WARN below
// (buildACLReconcilers) says so explicitly so this isn't mistaken for full
// coverage.
var newEffectiveAccessChecker = func() (macls.EffectiveAccessChecker, error) {
	iamClient, err := client.NewIAMClient()
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, principalArn string, actions, resources []string) (map[string]bool, error) {
		// Resolve the principal's permissions boundary (if any) ONCE, before
		// simulation — it's identical for every bucket simulateEffectiveAccess
		// simulates (only ResourceArns differs per bucket). A fetch error
		// fails the whole check rather than proceeding without the boundary,
		// which would under-restrict (report a boundary-capped grant as
		// allowed).
		var boundaryInputList []string
		boundaryDoc, present, err := iamservice.GetPrincipalPermissionsBoundaryDoc(ctx, iamClient, principalArn)
		if err != nil {
			return nil, fmt.Errorf("fetching permissions boundary for principal %s: %w", principalArn, err)
		}
		if present {
			boundaryInputList = []string{boundaryDoc}
		}

		return simulateEffectiveAccess(ctx, iamClient, principalArn, actions, resources, boundaryInputList)
	}, nil
}

// principalPolicySimulator is the minimal iam:SimulatePrincipalPolicy surface
// simulateEffectiveAccess needs. *awsiam.Client satisfies it structurally, so
// newEffectiveAccessChecker's real client passes through unchanged; tests
// substitute a fake to exercise the loop hermetically.
type principalPolicySimulator interface {
	SimulatePrincipalPolicy(ctx context.Context, in *awsiam.SimulatePrincipalPolicyInput, optFns ...func(*awsiam.Options)) (*awsiam.SimulatePrincipalPolicyOutput, error)
}

// simulateEffectiveAccess is the per-bucket SimulatePrincipalPolicy loop
// extracted from newEffectiveAccessChecker's closure (see its doc comment for
// the full rationale). It batches ALL of a principal's distinct actions x
// resources per bucket (ActionNames/ResourceArns), reads whichever of
// EvaluationResults[].ResourceSpecificResults or the top-level
// EvalResourceName/EvalDecision the SDK actually populated, keys the result
// action+"|"+resource to match macls.EffectiveAccessChecker's contract, and
// follows pagination (IsTruncated/Marker) to completion before returning.
//
// AWS's SimulatePrincipalPolicy rejects a ResourceArns list mixing the bare
// "*" wildcard with individual resource ARNs ("you cannot include both * and
// individual resources in the resource list"). splitResourceArnsForSimulation
// partitions resources into at most two buckets — the bare "*" (if present)
// and everything else — so each bucket is simulated in its own call, with its
// own fresh SimulatePrincipalPolicyInput (so Marker resets per bucket).
// ActionNames and boundaryPolicyDocs are identical across buckets; only
// ResourceArns differs. Every bucket's results are merged into the same
// returned map.
func simulateEffectiveAccess(ctx context.Context, sim principalPolicySimulator, principalArn string, actions, resources, boundaryPolicyDocs []string) (map[string]bool, error) {
	out := make(map[string]bool)

	for _, bucket := range splitResourceArnsForSimulation(resources) {
		input := &awsiam.SimulatePrincipalPolicyInput{
			PolicySourceArn:                    aws.String(principalArn),
			ActionNames:                        actions,
			ResourceArns:                       bucket,
			PermissionsBoundaryPolicyInputList: boundaryPolicyDocs,
		}

		// maxPages bounds the loop against a misbehaving server that
		// keeps returning IsTruncated=true with a nil/unchanged Marker,
		// rather than spinning forever. Mirrors the same guard in
		// internal/migrate/serviceaccounts/ccclient.go
		// (NumericToResourceID).
		const maxPages = 10000
		for page := 0; ; page++ {
			if page >= maxPages {
				return nil, fmt.Errorf("SimulatePrincipalPolicy pagination exceeded %d pages for principal %s", maxPages, principalArn)
			}
			resp, err := sim.SimulatePrincipalPolicy(ctx, input)
			if err != nil {
				return nil, fmt.Errorf("simulating effective IAM access for principal %s: %w", principalArn, err)
			}
			for _, result := range resp.EvaluationResults {
				action := aws.ToString(result.EvalActionName)
				if len(result.ResourceSpecificResults) > 0 {
					for _, rsr := range result.ResourceSpecificResults {
						resource := aws.ToString(rsr.EvalResourceName)
						out[action+"|"+resource] = rsr.EvalResourceDecision == awsiamtypes.PolicyEvaluationDecisionTypeAllowed
					}
					continue
				}
				resource := aws.ToString(result.EvalResourceName)
				out[action+"|"+resource] = result.EvalDecision == awsiamtypes.PolicyEvaluationDecisionTypeAllowed
			}
			if !resp.IsTruncated {
				break
			}
			input.Marker = resp.Marker
		}
	}
	return out, nil
}

// splitResourceArnsForSimulation partitions a principal's distinct resource
// ARNs into the buckets newEffectiveAccessChecker must simulate in SEPARATE
// SimulatePrincipalPolicyInput.ResourceArns calls: AWS forbids a single
// ResourceArns list from containing BOTH the bare wildcard "*" and any
// individual resource ARN ("you cannot include both * and individual
// resources in the resource list").
//
// Only the LITERAL string "*" is the special bare wildcard; an ARN that
// merely contains a "*" as part of its own value (e.g. a topic-name-prefix
// wildcard like "arn:...:topic/.../foo-*") is an individual resource ARN and
// stays in the specifics bucket — AWS's restriction is about the bare
// wildcard resource, not about the character.
//
// Returns:
//   - [["*"], [specific...]] when resources contains the bare "*" AND at
//     least one other (specific) resource,
//   - [[resources...]] unchanged (one bucket) when there is no bare "*" —
//     this is the pre-existing, unchanged behaviour,
//   - [["*"]] when resources is exactly the bare wildcard alone,
//   - nil for an empty/nil input.
//
// Specifics are returned in their original input order (stable, not
// resorted) — determinism here comes from resources itself already being
// the deduped, sorted output of distinctActionsAndResources
// (internal/migrate/acls/iam_effective.go), not from any reordering in this
// function.
func splitResourceArnsForSimulation(resources []string) [][]string {
	if len(resources) == 0 {
		return nil
	}

	hasWildcard := false
	specifics := make([]string, 0, len(resources))
	for _, r := range resources {
		if r == "*" {
			hasWildcard = true
			continue
		}
		specifics = append(specifics, r)
	}

	if !hasWildcard {
		return [][]string{resources}
	}
	if len(specifics) == 0 {
		return [][]string{{"*"}}
	}
	return [][]string{{"*"}, specifics}
}

// ccIAMBaseURL is the Confluent Cloud IAM v2 API base URL used to provision
// service accounts. It is a package-level var so tests can point it at a stub.
var ccIAMBaseURL = msa.DefaultBaseURL

func NewMigrateApplyCmd() *cobra.Command {
	var file string
	var dryRun bool

	cmd := &cobra.Command{
		Use:           "apply",
		Short:         "Apply a migration manifest (additively reconcile the target)",
		SilenceUsage:  true,
		SilenceErrors: true,
		PreRunE:       func(cmd *cobra.Command, _ []string) error { return utils.BindEnvToFlags(cmd) },
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runApply(cmd, file, dryRun)
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "Path to the migration manifest (required)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the changes without applying them")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

// aclSetupFailed is a reconcile.Reconciler that carries a captured
// ACL-track setup error. buildACLReconcilers does eager live I/O (source
// native-ACL read, AWS IAM gather/verify/translate, the CC numeric-principal
// map) during track assembly — before eng.Run. A failure there must NOT abort
// the whole command, because that would skip the independent clusterLink/topics
// track (engine.Run's cross-track isolation only covers failures INSIDE
// eng.Run). Modelling the setup failure as a one-reconciler track whose
// precondition fails lets the engine run the other track regardless while still
// surfacing this error (non-zero exit) via its joined error.
//
// The engine prefixes the returned error as "acls: precondition failed: %w", so
// CheckPreconditions returns the raw captured error (no double-wrap). Plan and
// Apply return it too, defensively — they are never reached because the engine
// stops the track at the failed precondition.
type aclSetupFailed struct{ err error }

func (aclSetupFailed) Name() string { return "acls" }

func (f aclSetupFailed) CheckPreconditions(context.Context) error { return f.err }

func (f aclSetupFailed) Plan(context.Context) (reconcile.Plan, error) { return nil, f.err }

func (f aclSetupFailed) Apply(context.Context, reconcile.Plan) (reconcile.Outcome, error) {
	return reconcile.Outcome{}, f.err
}

func runApply(cmd *cobra.Command, file string, dryRun bool) error {
	data, err := os.ReadFile(file)
	if err != nil {
		return fmt.Errorf("reading manifest: %w", err)
	}
	m, err := manifest.Parse(data)
	if err != nil {
		return err
	}
	if errs := m.Validate(); len(errs) > 0 {
		for _, e := range errs {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "✖ %v\n", e)
		}
		return fmt.Errorf("manifest is invalid: %d problem(s) found", len(errs))
	}

	// Phase 1 supports cluster-link, topics, and/or acls sections.
	if m.Spec.ClusterLink == nil && m.Spec.Topics == nil && m.Spec.ACLs == nil {
		return fmt.Errorf("nothing to apply: spec.clusterLink, spec.topics and/or spec.acls is required in this phase")
	}

	// --- source cluster id reader (spec.source → D1) ---
	// spec.source.credentials is used only to read the live source cluster id
	// (and, in destination mode, is independent of the link→source connection
	// auth, which comes from clusterLink.source).
	srcCluster, err := loadMigrateCluster(cmd, "spec.source", m.Spec.Source.BootstrapServers, m.Spec.Source.Credentials)
	if err != nil {
		return err
	}
	if err := ensureIAMAllowed(srcCluster, m.Spec.Source.Type, "spec.source.credentials", false); err != nil {
		return err
	}
	if err := ensureMSKScramMechanism(srcCluster, m.Spec.Source.Type, "spec.source.credentials"); err != nil {
		return err
	}
	src := newSourceReader(srcCluster)

	// --- destination target ---
	// The cluster credential drives the /kafka/v3 REST surface (cluster link,
	// topics, ACLs). The serviceAccounts reconciler needs a DIFFERENT credential
	// (spec.target.cloudCredentials, the CC Cloud/Global API key) loaded in
	// buildACLReconcilers, because IAM v2 rejects a Kafka cluster API key.
	tgtCreds, err := targets.LoadCredentials(m.Spec.Target.ClusterCredentials)
	if err != nil {
		return err
	}
	tgtClient, err := tgtCreds.HTTPClient()
	if err != nil {
		return err
	}
	var tgt *targets.LinkEndpoint
	switch m.Spec.Target.Type {
	case manifest.TargetConfluentPlatform:
		tgt = targets.NewConfluentPlatformTarget(m.Spec.Target.Kafka.RestEndpoint, tgtCreds, tgtClient)
	case manifest.TargetConfluentCloud:
		tgt, err = targets.NewConfluentCloudTarget(m.Spec.Target.Kafka.RestEndpoint, m.Spec.Target.ClusterID, tgtCreds, tgtClient)
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported target type %q", m.Spec.Target.Type)
	}

	// --- reconcilers ---
	// The engine runs reconcilers in order; the cluster link (when present) is the
	// precondition for mirror topics, so it is appended first.
	var recs []reconcile.Reconciler

	// cl and mode are retained for the topic wiring below; both are zero when
	// no cluster-link section is present (mode:new can run without a link).
	var cl *manifest.ClusterLink
	var mode string
	// srcLinkTgt is the source-side OUTBOUND link endpoint (source-initiated mode
	// only). It carries cluster.link.prefix, so it is the prefix target for mirror
	// topics in source mode. nil in destination mode.
	var srcLinkTgt *targets.LinkEndpoint

	if m.Spec.ClusterLink != nil {
		cl = m.Spec.ClusterLink
		mode = cl.Mode
		if mode == "" {
			mode = manifest.ClusterLinkModeDestination
		}

		linkConfigs, err := cl.ResolvedLinkConfigs()
		if err != nil {
			return fmt.Errorf("resolving cluster-link configs: %w", err)
		}

		var rec *mclusterlink.Reconciler
		switch mode {
		case manifest.ClusterLinkModeDestination:
			// Destination-initiated: the link→source connection auth comes from
			// clusterLink.source (D2), NOT spec.source.
			// Defensive: Validate() (run above) already requires clusterLink.source
			// in destination mode, so this can't fire today — but guard the deref so
			// a future validation change can't turn it into a nil panic.
			if cl.Source == nil {
				return fmt.Errorf("clusterLink.source is required for mode %q", manifest.ClusterLinkModeDestination)
			}
			linkCluster, err := loadMigrateCluster(cmd, "spec.clusterLink.source", cl.Source.BootstrapServers, cl.Source.Credentials)
			if err != nil {
				return err
			}
			if err := ensureIAMAllowed(linkCluster, m.Spec.Source.Type, "spec.clusterLink.source.credentials", true); err != nil {
				return err
			}
			if err := ensureMSKScramMechanism(linkCluster, m.Spec.Source.Type, "spec.clusterLink.source.credentials"); err != nil {
				return err
			}
			auth, err := mclusterlink.LinkAuthFromSource(linkCluster)
			if err != nil {
				return fmt.Errorf("deriving cluster-link source auth: %w", err)
			}
			rec = mclusterlink.New(mclusterlink.Config{
				LinkName:               cl.Name,
				Mode:                   manifest.ClusterLinkModeDestination,
				SourceBootstrapServers: cl.Source.BootstrapServers,
				Auth:                   auth,
				Configs:                linkConfigs,
			}, src, tgt)

		case manifest.ClusterLinkModeSource:
			// Source-initiated: a second link object lives on the source cluster's
			// REST (D4). It carries the destination address + source→destination
			// connection auth derived from clusterLink.destination (D5).
			if cl.SourceRest == nil {
				return fmt.Errorf("clusterLink.sourceRest is required for mode %q", manifest.ClusterLinkModeSource)
			}
			srcRestCreds, err := targets.LoadCredentials(cl.SourceRest.Credentials)
			if err != nil {
				return err
			}
			srcRestClient, err := srcRestCreds.HTTPClient()
			if err != nil {
				return err
			}
			srcLinkTgt = targets.NewLinkEndpoint(cl.SourceRest.Endpoint, srcRestCreds, srcRestClient)

			// Defensive: Validate() already requires clusterLink.destination in
			// source mode; guard the deref against a future validation change.
			if cl.Destination == nil {
				return fmt.Errorf("clusterLink.destination is required for mode %q", manifest.ClusterLinkModeSource)
			}
			destCluster, err := loadMigrateCluster(cmd, "spec.clusterLink.destination", cl.Destination.BootstrapServers, cl.Destination.Credentials)
			if err != nil {
				return err
			}
			if err := ensureIAMAllowed(destCluster, m.Spec.Source.Type, "spec.clusterLink.destination.credentials", true); err != nil {
				return err
			}
			destAuth, err := mclusterlink.LinkAuthFromSource(destCluster)
			if err != nil {
				return fmt.Errorf("deriving cluster-link destination auth: %w", err)
			}
			rec = mclusterlink.NewSourceInitiated(mclusterlink.Config{
				LinkName:             cl.Name,
				Mode:                 manifest.ClusterLinkModeSource,
				DestBootstrapServers: cl.Destination.BootstrapServers,
				DestAuth:             destAuth,
				Configs:              linkConfigs,
			}, src, tgt, srcLinkTgt)

		default:
			return fmt.Errorf("unsupported clusterLink.mode %q", mode)
		}
		recs = append(recs, rec)
	}

	if t := m.Spec.Topics; t != nil {
		switch t.Mode {
		case manifest.TopicModeMirror:
			// Validation guarantees a cluster link is present for mode:mirror;
			// guard defensively in case this is reached without one.
			if cl == nil {
				return fmt.Errorf("spec.topics.mode %q requires spec.clusterLink", manifest.TopicModeMirror)
			}
			// The prefix (cluster.link.prefix) lives on the link object. In
			// destination mode that is the destination target; in source mode it is
			// the source-side OUTBOUND link.
			prefixTgt := tgt
			if mode == manifest.ClusterLinkModeSource {
				prefixTgt = srcLinkTgt
			}
			recs = append(recs, mmirror.New(mmirror.Config{
				LinkName: cl.Name,
				Include:  t.Include,
				Exclude:  t.Exclude,
				Prefix:   cl.Prefix,
			}, src, tgt, prefixTgt))

		case manifest.TopicModeNew:
			recs = append(recs, mnew.New(mnew.Config{
				Include: t.Include,
				Exclude: t.Exclude,
			}, src, tgt))

		default:
			return fmt.Errorf("unsupported topics.mode %q", t.Mode)
		}
	}

	// Group reconcilers into independent dependency TRACKS (see engine.Run):
	//   - clusterLink → topics: mirror topics depend on the link (one chain).
	//   - serviceAccounts → acls: acls depend on the resolved service accounts.
	// The two tracks are independent, so a partial failure in one (e.g. some
	// mirror topics 403 because the link's source creds lack ACLs) must NOT skip
	// the other — the ACL migration still runs. Within a track it stays fail-fast.
	var tracks [][]reconcile.Reconciler
	if len(recs) > 0 {
		tracks = append(tracks, recs) // clusterLink + topics
	}

	// ACL migration: READ (source) -> PROVISION (service accounts) -> WRITE
	// (ACLs), in that order so service accounts are resolved before ACLs are
	// written. Its own track — independent of clusterLink/topics.
	if m.Spec.ACLs != nil {
		aclRecs, err := buildACLReconcilers(cmd, m, srcCluster, tgtClient, tgtCreds)
		if err != nil {
			// buildACLReconcilers does eager live I/O during track assembly. A
			// setup failure here must NOT abort the command and skip the
			// independent clusterLink/topics track — so model it as its own
			// track whose single reconciler fails its precondition. eng.Run's
			// per-track isolation then runs track A regardless, while its joined
			// error still surfaces this failure (non-zero exit).
			tracks = append(tracks, []reconcile.Reconciler{aclSetupFailed{err}})
		} else {
			tracks = append(tracks, aclRecs)
		}
	}

	eng := reconcile.NewEngine(cmd.OutOrStdout())
	// Phase 1 relies on the engine to render outcomes; the structured Report is
	// not consumed yet (a later phase may use it for a machine-readable summary).
	_, err = eng.Run(cmd.Context(), tracks, dryRun)
	return err
}

// buildACLReconcilers runs the source-read + normalization stages inline
// (printing diagnostics to the operator) and returns the serviceAccounts and
// acls reconcilers, in that order, for the engine to Plan/Apply. The acls
// reconciler reads the serviceAccounts reconciler's resolved map lazily at its
// own Plan time (late-binding), so in apply mode it observes the real
// "User:sa-<id>" ids the provision stage produced.
func buildACLReconcilers(cmd *cobra.Command, m *manifest.Migration, srcCluster types.KafkaSourceConn, tgtClient clusterlink.HTTPClient, tgtCreds *targets.Credentials) ([]reconcile.Reconciler, error) {
	// READ: list native ACLs from the source.
	adm, err := newSourceACLLister(srcCluster)
	if err != nil {
		return nil, fmt.Errorf("connecting to source for ACL read: %w", err)
	}
	defer func() { _ = adm.Close() }()
	raw, err := macls.ReadNativeACLs(adm)
	if err != nil {
		return nil, fmt.Errorf("reading source ACLs: %w", err)
	}

	// READ (IAM plane): union in the IAM-derived ACL equivalents for the
	// source principals. Two mutually-exclusive gather modes are supported —
	// explicit spec.acls.iam.principalArns, or spec.acls.iam.discoverAllRoles
	// (whole-account role enumeration via GetAccountAuthorizationDetails) —
	// plus an optional spec.acls.iam.verifyEffectiveAccess filter
	// (SimulatePrincipalPolicy) that applies to either gather mode's results.
	//
	// A cross-plane duplicate tuple (native and IAM-derived ACLs resolving to
	// the identical types.Acls value), or a within-plane duplicate (e.g. two
	// IAM policy statements granting the same op/resource), is NOT
	// deduplicated by NormalizeForCC — its stage-4 only drops a Describe
	// implied by a broader op, not literal duplicates — nor by the acls
	// reconciler, whose map[types.Acls]struct{} diff (reconciler.go Plan) is
	// built solely from the TARGET's existing ACLs, never accumulated across
	// entries already staged from Desired. Per the Phase 1B design
	// ("union + dedupe onto the target"), dedupe is folded in below at the
	// normalized-set boundary (dedupeACLs), which uniformly covers all three
	// duplicate shapes — native-internal, IAM-internal, and cross-plane.
	if iam := m.Spec.ACLs.IAM; iam != nil {
		// GATHER: either enumerate every account role (discoverAllRoles) or
		// fetch the fixed, explicitly-named principals — mutually exclusive
		// per manifest validation (internal/manifest/validate.go), so only
		// the seam the selected mode actually needs is built.
		var pps []iamservice.PrincipalPolicies
		if iam.DiscoverAllRoles {
			enumerate, err := newRolePolicyEnumerator()
			if err != nil {
				return nil, fmt.Errorf("building IAM client for role enumeration: %w", err)
			}
			pps, err = macls.GatherEnumerated(cmd.Context(), enumerate)
			if err != nil {
				return nil, fmt.Errorf("enumerating IAM account roles: %w", err)
			}
		} else {
			fetch, err := newIAMFetcher()
			if err != nil {
				return nil, fmt.Errorf("building IAM client: %w", err)
			}
			pps, err = macls.GatherExplicit(cmd.Context(), fetch, iam.PrincipalArns)
			if err != nil {
				return nil, fmt.Errorf("reading IAM-derived ACLs: %w", err)
			}
		}

		// VERIFY (optional): drop grants SimulatePrincipalPolicy reports as
		// not effectively allowed against the principal's identity policies
		// and (for a role principal) its permissions boundary, for BOTH
		// gather modes. SCPs are never evaluated by SimulatePrincipalPolicy —
		// see the WARN below and newEffectiveAccessChecker's doc comment.
		if iam.VerifyEffectiveAccess {
			check, err := newEffectiveAccessChecker()
			if err != nil {
				return nil, fmt.Errorf("building IAM client for effective-access verification: %w", err)
			}
			pps, err = macls.FilterEffective(cmd.Context(), check, iam.ClusterArn, pps)
			if err != nil {
				return nil, fmt.Errorf("verifying effective IAM access: %w", err)
			}
		}

		// TRANSLATE: the surviving principal policies into their ACL
		// equivalents, cluster-scoped.
		iamAcls := macls.TranslatePrincipalPolicies(iam.ClusterArn, pps)
		if len(iamAcls) == 0 {
			slog.Warn("⚠️ spec.acls.iam matched zero ACLs — no kafka-cluster grants found for the source principals scoped to clusterArn; check principalArns/discoverAllRoles scope and clusterArn")
		}
		raw = append(raw, iamAcls...)
		// Both branches warn, honestly: verification off means effective
		// access was never checked at all; verification on still leaves SCPs
		// unevaluated (SimulatePrincipalPolicy has no input for them), so
		// neither state is "fully verified" and neither WARN should imply
		// it.
		if iam.VerifyEffectiveAccess {
			slog.Warn("⚠️ effective access verified via SimulatePrincipalPolicy against identity policies and permission boundaries; service control policies (SCPs) are NOT evaluated and may further restrict access on the source")
		} else {
			slog.Warn("⚠️ IAM-derived ACLs are granted by identity policy; effective access (SCP/permission-boundary/deny) not verified — set spec.acls.iam.verifyEffectiveAccess to confirm")
		}
	}

	// Filter by spec.acls include/exclude (glob against principal and resource
	// name), then normalize the survivors for the Confluent Cloud target.
	filtered, err := filterACLs(raw, m.Spec.ACLs.Include, m.Spec.ACLs.Exclude)
	if err != nil {
		return nil, fmt.Errorf("filtering ACLs: %w", err)
	}
	norm, diags := macls.NormalizeForCC(filtered)
	logACLDiagnostics(diags)
	norm = dedupeACLs(norm)

	// World-open topics policy (allow.everyone.if.no.acl.found).
	if err := checkUnprotectedTopics(m); err != nil {
		return nil, err
	}

	principals := distinctPrincipals(norm)

	// PROVISION: service accounts on the Confluent Cloud target. The IAM v2 API
	// (ccIAMBaseURL / api.confluent.cloud) and the legacy /service_accounts list
	// both require a Cloud/Global API key — distinct from the Kafka cluster API
	// key the ACL client uses — so the SA client is built from
	// spec.target.cloudCredentials, not the cluster credential. For a
	// Confluent Cloud target the Cloud/Global creds are loaded for EVERY
	// spec.acls run (this function's entry condition), because they serve two
	// purposes: serviceAccounts.autoCreate provisions accounts via IAM v2, and —
	// regardless of autoCreate — the acls reconciler needs the numeric-id map
	// (built below) to stay idempotent. A non-Confluent-Cloud target keeps the
	// cluster client as a harmless fallback and never loads cloud creds.
	var saCfg msa.Config
	if sa := m.Spec.ServiceAccounts; sa != nil {
		saCfg = msa.Config{AutoCreate: sa.AutoCreate, Mapping: sa.Mapping}
	}
	saCfg.Principals = principals
	saClient, saAuth := tgtClient, tgtCreds.Authenticator()
	cloudCredsAvailable := false
	if m.Spec.Target.Type == manifest.TargetConfluentCloud {
		cloudCreds, err := targets.LoadCredentials(m.Spec.Target.CloudCredentials)
		if err != nil {
			return nil, fmt.Errorf("loading spec.target.cloudCredentials: %w", err)
		}
		cloudClient, err := cloudCreds.HTTPClient()
		if err != nil {
			return nil, fmt.Errorf("building cloud HTTP client: %w", err)
		}
		saClient, saAuth = cloudClient, cloudCreds.Authenticator()
		cloudCredsAvailable = true
	}
	saCC := msa.NewCCClient(ccIAMBaseURL, saClient, saAuth)
	saCfg.Client = saCC

	// Build the numeric-id -> "sa-" resource-id map for a Confluent Cloud
	// target. Confluent Cloud accepts an ACL created with principal
	// "User:sa-abc123" but returns it on read-back as the service account's
	// internal numeric id ("User:1267635"); the acls reconciler uses this map to
	// normalize read-back principals before diffing, without which it never
	// detects an existing ACL as present and re-creates every ACL on each apply.
	// Confluent Cloud returns the numeric form for ANY ACL it stores — whether
	// the account was auto-created OR mapped to a pre-existing "sa-" id — so
	// idempotency ALWAYS needs this map for a Confluent Cloud target, which is
	// why the Cloud/Global creds are loaded above for every spec.acls run (not
	// only the autoCreate path). The legacy /service_accounts endpoint rejects a
	// Kafka cluster key, hence those creds. A non-Confluent-Cloud target leaves
	// the map empty, which disables normalization (not needed there).
	var numericToResourceID map[string]string
	if cloudCredsAvailable {
		numericToResourceID, err = saCC.NumericToResourceID(cmd.Context())
		if err != nil {
			return nil, fmt.Errorf("mapping service-account numeric ids: %w", err)
		}
		// The same fetched service-account set doubles as the existence guard for
		// serviceAccounts.mapping targets: its values are exactly the "sa-" ids
		// that exist on the target. A non-nil set switches on mapped-id validation
		// in the reconciler (Confluent Cloud only) so a typo'd/stale mapping fails
		// loud instead of creating orphaned ACLs Confluent Cloud won't reject.
		existingSAIDs := make(map[string]bool, len(numericToResourceID))
		for _, resourceID := range numericToResourceID {
			existingSAIDs[resourceID] = true
		}
		saCfg.ExistingSAIDs = existingSAIDs
	}
	saRec := msa.New(saCfg)

	// WRITE: ACLs on the target REST endpoint. ResolvedPrincipals is the SA
	// reconciler's live map (late-bound at acls.Plan time); NumericToResourceID
	// normalizes read-back principals so present-detection is idempotent.
	aclClient := macls.NewACLClient(m.Spec.Target.Kafka.RestEndpoint, m.Spec.Target.ClusterID, tgtClient, tgtCreds.Authenticator())
	aclRec := macls.New(macls.Config{
		Desired:             norm,
		ResolvedPrincipals:  saRec.ResolvedMap,
		NumericToResourceID: numericToResourceID,
		Client:              aclClient,
	})

	return []reconcile.Reconciler{saRec, aclRec}, nil
}

// filterACLs keeps an ACL when its principal or resource name matches an
// include glob and neither matches an exclude glob (exclude always wins). It
// mirrors topicselect's path.Match semantics but omits the internal-topic
// opt-in logic, which is not meaningful for ACL principals/resources.
func filterACLs(in []types.Acls, include, exclude []string) ([]types.Acls, error) {
	matchAny := func(pats []string, fields ...string) (bool, error) {
		for _, p := range pats {
			for _, f := range fields {
				ok, err := path.Match(p, f)
				if err != nil {
					return false, err
				}
				if ok {
					return true, nil
				}
			}
		}
		return false, nil
	}

	var out []types.Acls
	for _, a := range in {
		inc, err := matchAny(include, a.Principal, a.ResourceName)
		if err != nil {
			return nil, err
		}
		if !inc {
			continue
		}
		exc, err := matchAny(exclude, a.Principal, a.ResourceName)
		if err != nil {
			return nil, err
		}
		if exc {
			continue
		}
		out = append(out, a)
	}
	return out, nil
}

// dedupeACLs collapses acls to its distinct types.Acls tuples, preserving
// first-occurrence order. types.Acls is all-string-fields and fully
// comparable, so it doubles as its own map key. This closes the "additive
// union + dedupe" half of the Phase 1B design: without it, a tuple appearing
// more than once in the normalized set — whether from two IAM policy
// statements granting the same op/resource, or from the native and IAM
// planes resolving to the identical tuple for the same principal — is
// planned and applied once per occurrence, because the acls reconciler's
// diff (reconciler.go Plan) only compares Desired against the target's
// EXISTING ACLs and never against duplicates within Desired itself.
func dedupeACLs(acls []types.Acls) []types.Acls {
	seen := make(map[types.Acls]struct{}, len(acls))
	out := make([]types.Acls, 0, len(acls))
	for _, a := range acls {
		if _, ok := seen[a]; ok {
			continue
		}
		seen[a] = struct{}{}
		out = append(out, a)
	}
	return out
}

// distinctPrincipals returns the sorted, de-duplicated set of principals across
// the normalized ACL set.
func distinctPrincipals(acls []types.Acls) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, a := range acls {
		if _, ok := seen[a.Principal]; ok {
			continue
		}
		seen[a.Principal] = struct{}{}
		out = append(out, a.Principal)
	}
	sort.Strings(out)
	return out
}

// logACLDiagnostics emits NormalizeForCC diagnostics through slog (the standard
// two-leg model, PR #382 / CLAUDE.md) rather than bespoke fmt output, so they
// render identically to the rest of the command: warn/error surface on the
// console (coloured level) and in kcp.log; info (benign drops — drop-list,
// dedup) go to kcp.log and only reach the console under --verbose. Per the
// project emoji standard, warnings lead with ⚠️ and skip/drop notes with ⏭️.
func logACLDiagnostics(diags []macls.Diagnostic) {
	for _, d := range diags {
		switch d.Level {
		case "warn", "error":
			slog.Warn("⚠️ " + d.Message)
		default:
			slog.Info("⏭️ " + d.Message)
		}
	}
}

// checkUnprotectedTopics applies spec.acls.unprotectedTopicPolicy to the
// source's allow.everyone.if.no.acl.found setting.
//
// The live read of that setting is only meaningful for an MSK source, where it
// comes from the cluster's config-revision server.properties. That read is not
// wired here: the config-revision ARN is not carried in the manifest (spec.source
// holds type/bootstrapServers/credentials only), so acls.AllowEveryoneIfNoACLFound
// has no server.properties to evaluate in this task.
//
// Because detection is not implemented, this function must not pretend the
// policy is enforced: it always surfaces a terminal warning that world-open
// detection is unverified for MSK sources, and when the operator explicitly
// asked for a hard stop (policy=fail) it fails closed — refusing to proceed —
// rather than silently honoring a safety control it cannot actually check.
func checkUnprotectedTopics(m *manifest.Migration) error {
	if m.Spec.Source.Type != manifest.SourceMSK || m.Spec.ACLs == nil {
		return nil
	}
	policy := m.Spec.ACLs.UnprotectedTopicPolicy
	if policy == "" {
		policy = manifest.UnprotectedTopicPolicyWarn
	}

	slog.Warn("⚠️ world-open-topic detection (allow.everyone.if.no.acl.found) is not yet enforced for MSK sources — verify this setting manually on the source before relying on this migration for authorization completeness")

	if policy == manifest.UnprotectedTopicPolicyFail {
		return fmt.Errorf("spec.acls.unprotectedTopicPolicy: %q requested, but world-open-topic detection (allow.everyone.if.no.acl.found) is not yet implemented — the requested hard stop cannot be honored; verify the source setting manually, then adjust the policy to proceed", manifest.UnprotectedTopicPolicyFail)
	}
	return nil
}

// ensureIAMAllowed rejects IAM where it cannot work: a cluster link can never
// authenticate via IAM (isLinkCreds), and IAM as a source-read method requires
// an MSK source. Non-IAM auth always passes. A selection error is left for the
// downstream loader/reader to surface.
// ensureMSKScramMechanism rejects a SCRAM connection to an MSK source that is not
// SHA-512. MSK's SCRAM is SHA-512-only, so SHA-256 (the read-path default for an
// unspecified mechanism) fails auth with an opaque "ran out of brokers" error.
// Only meaningful for an MSK source; other source types keep their own defaults.
func ensureMSKScramMechanism(conn types.KafkaSourceConn, sourceType, field string) error {
	if sourceType != manifest.SourceMSK {
		return nil
	}
	at, err := conn.GetSelectedAuthType()
	if err != nil || at != types.AuthTypeSASLSCRAM {
		return nil
	}
	if types.NormalizeSaslMechanism(conn.AuthMethod.SASLScram.Mechanism) != "SCRAM-SHA-512" {
		return fmt.Errorf("%s: MSK requires SCRAM-SHA-512 (set mechanism: SHA512), got %q", field, conn.AuthMethod.SASLScram.Mechanism)
	}
	return nil
}

func ensureIAMAllowed(conn types.KafkaSourceConn, sourceType, field string, isLinkCreds bool) error {
	at, err := conn.GetSelectedAuthType()
	if err != nil || at != types.AuthTypeIAM {
		return nil
	}
	if isLinkCreds {
		return fmt.Errorf("%s: iam cannot authenticate a cluster link; use sasl_scram or mtls", field)
	}
	if sourceType != manifest.SourceMSK {
		return fmt.Errorf("%s: iam auth requires spec.source.type: msk (got %q)", field, sourceType)
	}
	return nil
}

// loadMigrateCluster loads + validates a flat migrate credentials file and composes
// the result with the given bootstrap servers from the manifest into a KafkaSourceConn.
func loadMigrateCluster(cmd *cobra.Command, field string, bootstrapServers []string, path string) (types.KafkaSourceConn, error) {
	if path == "" {
		return types.KafkaSourceConn{}, fmt.Errorf("%s.credentials is required", field)
	}
	creds, errs := types.LoadMigrateClusterCredentials(path)
	if len(errs) > 0 {
		for _, e := range errs {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "✖ %v\n", e)
		}
		return types.KafkaSourceConn{}, fmt.Errorf("invalid %s.credentials: %d problem(s) found", field, len(errs))
	}
	return types.MigrateConn(bootstrapServers, creds), nil
}
