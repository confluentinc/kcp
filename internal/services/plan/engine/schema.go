package engine

// Block 6 — Schema: the source registry type folds edition and version into one
// signal, and the strategy is intent. Together they pick the method: Schema
// Linking, Replicator, Glue bulk re-registration, a fresh registry, schemaless,
// or a tech-assist handoff.

// Source Schema Registry types, in reading order.
const (
	sourceSRNone          = "None"
	sourceSRGlue          = "AWS Glue Schema Registry"
	sourceSRCPEnterprise7 = "Confluent Schema Registry: Confluent Platform Enterprise 7.1 or later"
	sourceSRCPCommunity   = "Confluent Schema Registry: Community, or Confluent Platform below 7.1"
	sourceSROther         = "Other or not sure"
)

// Schema strategies.
const (
	schemaStrategyMigrate    = "Migrate my existing schemas"
	schemaStrategyFresh      = "Start fresh on Confluent Cloud"
	schemaStrategySchemaless = "Stay schemaless"
)

// SchemaKind is the stable typed identity of a Schema verdict, so the assembler
// (citation wiring) and renderer switch on it rather than on the display Value
// copy. Not serialized (json:"-") to keep plan.json byte-identical.
type SchemaKind string

const (
	SchemaKindPending    SchemaKind = "pending"
	SchemaKindSchemaless SchemaKind = "schemaless"
	SchemaKindFresh      SchemaKind = "fresh"
	SchemaKindLinking    SchemaKind = "schema_linking"
	SchemaKindGlueBulk   SchemaKind = "glue_bulk"
	SchemaKindReplicator SchemaKind = "replicator"
	SchemaKindSpecial    SchemaKind = "special"
)

// SchemaResult is the schema-migration verdict.
type SchemaResult struct {
	Value        string     `json:"value"`
	Pending      bool       `json:"pending,omitempty"` // set at JSON marshal when a deciding question is unanswered; Value is blanked
	Kind         SchemaKind `json:"-"`
	Held         bool       `json:"held,omitempty"` // blocked on a required answer (symmetric with SwitchoverResult.Held)
	Reason       string     `json:"reason"`
	How          string     `json:"how,omitempty"`
	Action       *string    `json:"action"`
	Tool         *string    `json:"tool,omitempty"`
	Pros         []string   `json:"pros,omitempty"`
	Cons         []string   `json:"cons,omitempty"`
	TechAssist   bool       `json:"tech_assist,omitempty"`
	OpenQuestion string     `json:"open_question,omitempty"`
	Source       string     `json:"source,omitempty"`
}

// isGovCloud disqualifies the exporter (forces the Replicator path). No profile
// signal exists yet, so this defaults to false.
func isGovCloud(p Profile) bool { return p.TargetIsGovCloud == "Yes" }

func schemaDecision(p Profile) SchemaResult {
	sr, strategy := p.SourceSRType, p.SchemaStrategy
	gov := isGovCloud(p)

	recreateFresh := func() SchemaResult {
		return SchemaResult{
			Value:  "Start with a fresh Schema Registry",
			Kind:   SchemaKindFresh,
			Reason: basis(ans("start fresh")) + "we provision Confluent Cloud Schema Registry and register new schemas during the migration rather than carrying your existing ones over.",
			Action: strptr("Create Schema Registry"),
			Pros:   []string{"A clean Confluent Cloud Schema Registry, set up during the migration"},
			Cons:   []string{"Schema IDs are not preserved", "Existing schema history is dropped"},
		}
	}
	replicator := func(reason string) SchemaResult {
		return SchemaResult{
			Value:      "Replicator",
			Kind:       SchemaKindReplicator,
			Reason:     reason,
			Action:     strptr("Set up Replicator"),
			TechAssist: true,
			Pros:       []string{"Preserves your existing schema IDs", "Works when your registry cannot reach Confluent Cloud directly"},
			Cons: []string{
				"Part of Confluent Platform Enterprise, so it needs a license",
				"Replicates the `_schemas` topic into Confluent Cloud Schema Registry in IMPORT mode",
				"IMPORT mode needs an empty destination registry, so plan this before you register anything else there",
			},
		}
	}
	notSet := func(reason string) SchemaResult {
		// "Pending" + Held mirror SwitchoverResult so both held verdicts read the
		// same in plan.json (md already shows both as "Pending" via the contingent map).
		return SchemaResult{Value: "Pending", Kind: SchemaKindPending, Held: true, Reason: reason}
	}

	// 1. No source registry.
	if sr == sourceSRNone || sr == "" {
		switch strategy {
		case schemaStrategySchemaless:
			return SchemaResult{
				Value:        "Schemaless",
				Kind:         SchemaKindSchemaless,
				Reason:       basis(srcOr(p.SchemaAnswered, "no Schema Registry detected")) + "with no Schema Registry, we skip the schema steps.",
				OpenQuestion: "Confirm the source is genuinely schemaless, or that the Schema Registry scan was run, before we lock this in.",
			}
		case schemaStrategyFresh:
			return SchemaResult{
				Value:  "Start with a fresh Schema Registry",
				Kind:   SchemaKindFresh,
				Reason: basis(srcOr(p.SchemaAnswered, "no source Schema Registry"), ans("start fresh")) + "we provision Confluent Cloud Schema Registry and register your schemas during the migration.",
				Action: strptr("Create Schema Registry"),
				Pros:   []string{"A clean Confluent Cloud Schema Registry, set up during the migration"},
				Cons:   []string{"Schema IDs start fresh, so there are none to carry over", "Client serializers need to point at Confluent Cloud Schema Registry"},
			}
		}
		return notSet("Tell us what you want to do with your schemas on Confluent Cloud to get a recommendation.")
	}

	// 2. AWS Glue Schema Registry.
	if sr == sourceSRGlue {
		if strategy == schemaStrategyFresh {
			return recreateFresh()
		}
		if strategy == schemaStrategySchemaless {
			return SchemaResult{
				Value:        "Schemaless (mismatch)",
				Kind:         SchemaKindSchemaless,
				Reason:       basis(srcOr(p.SchemaAnswered, "AWS Glue Schema Registry"), ans("schemaless")) + "you declared schemaless, but a Schema Registry is present on your source.",
				OpenQuestion: "Confirm you really want to drop your existing schemas and run schemaless.",
			}
		}
		if strategy == schemaStrategyMigrate {
			return SchemaResult{
				Value:  "Glue bulk re-registration",
				Kind:   SchemaKindGlueBulk,
				Reason: basis(srcOr(p.SchemaAnswered, "AWS Glue Schema Registry"), ans("migrate your schemas")) + "Glue schemas use a different wire format and cannot be linked, so we bulk re-register them instead. This does not preserve schema IDs, so plan a phased client cutover.",
				How:    "Glue bulk re-registration re-registers your Glue schemas into Confluent Cloud Schema Registry using Confluent's migration tool (kcp).",
				Action: strptr("Migrate Glue schemas"),
				Tool:   strptr("kcp create-asset migrate-schemas --glue-registry"),
				Pros:   []string{"Automated re-registration of your Glue schemas", "Works where Schema Linking cannot, since Glue cannot be linked"},
				Cons:   []string{"Schema IDs are not preserved", "Consumers run bilingual and producers switch over in a phased client cutover, which can take weeks", "Your subject naming strategy must match on Confluent Cloud"},
			}
		}
		return notSet("Tell us what you want to do with your schemas on Confluent Cloud to get a recommendation.")
	}

	// 3. Confluent SR — CP Enterprise 7.1+.
	if sr == sourceSRCPEnterprise7 {
		if strategy == schemaStrategyFresh {
			return recreateFresh()
		}
		if strategy == schemaStrategySchemaless {
			return SchemaResult{
				Value:        "Schemaless (mismatch)",
				Kind:         SchemaKindSchemaless,
				Reason:       basis(srcOr(p.SchemaAnswered, "Schema Registry detected"), ans("schemaless")) + "you declared schemaless, but a Schema Registry is present on your source.",
				OpenQuestion: "Confirm you really want to drop your existing schemas and run schemaless.",
			}
		}
		if strategy == schemaStrategyMigrate {
			if p.SourceSROutboundReachableToCC == "Yes" && !gov {
				return SchemaResult{
					Value:  "Schema Linking",
					Kind:   SchemaKindLinking,
					Reason: basis(srcOr(p.SchemaAnswered, "Confluent Platform Enterprise 7.1+"), ans("migrate your schemas")) + "your registry can reach Confluent Cloud, so we recommend Schema Linking. It preserves your schema IDs, and no client change is needed for the schema move.",
					How:    "Schema Linking mirrors your schemas live from your source Schema Registry into Confluent Cloud Schema Registry.",
					Action: strptr("Set up Schema Linking"),
					Tool:   strptr("kcp create-asset migrate-schemas --url"),
					Pros:   []string{"A live mirror that keeps Confluent Cloud in sync with your source", "Preserves your existing schema IDs", "No client change needed for the schema move"},
					Cons:   []string{"Your Schema Registry needs outbound reach to Confluent Cloud on port 443", "The target Schema Registry is set to IMPORT mode", "A dedicated context is needed if the target already holds schemas"},
				}
			}
			return replicator(basis(srcOr(p.SchemaAnswered, "Confluent Platform Enterprise 7.1+"), ans("migrate your schemas")) + "the schema exporter is unavailable here, because your registry cannot reach Confluent Cloud or this is a government cloud. Replicator replicates your `_schemas` topic into Confluent Cloud Schema Registry in IMPORT mode and preserves your schema IDs. Replicator is part of Confluent Platform Enterprise, which you already run, so it needs a license rather than an edition change. Contact us and we'll help.")
		}
		return notSet("Tell us what you want to do with your schemas on Confluent Cloud to get a recommendation.")
	}

	// 4. Confluent SR — Community or below 7.1.
	if sr == sourceSRCPCommunity {
		if strategy == schemaStrategyFresh {
			return recreateFresh()
		}
		if strategy == schemaStrategySchemaless {
			return SchemaResult{
				Value:        "Schemaless (mismatch)",
				Kind:         SchemaKindSchemaless,
				Reason:       basis(srcOr(p.SchemaAnswered, "Schema Registry detected"), ans("schemaless")) + "you declared schemaless, but a Schema Registry is present on your source.",
				OpenQuestion: "Confirm you really want to drop your existing schemas and run schemaless.",
			}
		}
		if strategy == schemaStrategyMigrate {
			return replicator(basis(srcOr(p.SchemaAnswered, "Confluent Community Schema Registry"), ans("migrate your schemas")) + "the schema exporter is Confluent Platform Enterprise 7.1+ only, so Replicator is the method for carrying your existing schemas across. It replicates your `_schemas` topic into Confluent Cloud Schema Registry in IMPORT mode and preserves your schema IDs. Replicator is also a Confluent Platform Enterprise component, so it needs a license that Community edition does not include. Contact us and we'll help. Alternatively, starting fresh on Confluent Cloud Schema Registry needs no license at all.")
		}
		return notSet("Tell us what you want to do with your schemas on Confluent Cloud to get a recommendation.")
	}

	// 5. Other or not sure — no method to plan.
	if sr == sourceSROther {
		return SchemaResult{
			Value:      "Special handling",
			Kind:       SchemaKindSpecial,
			Reason:     basis(srcOr(p.SchemaAnswered, "a third-party or unidentified Schema Registry")) + "there's no automated Schema Linking or kcp path for it. Getting your schemas into Confluent Cloud Schema Registry is something you set up on your side, and it's yours to run.",
			TechAssist: true,
		}
	}

	// 6. Registry type not set.
	return notSet("Tell us which Schema Registry your source environment uses to get a recommendation.")
}
