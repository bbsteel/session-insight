// Package presentation is a dependency-leaf contract for adapter-owned
// terminal presentation declarations.
//
// It may depend on the standard library only. It must not import reader,
// render, server, or frontend packages so the registry can aggregate
// adapter-exported declarations without import cycles.
package presentation

// SchemaVersion is the current declaration schema. Older work-order schemas
// are rejected by Reference Manager, not guessed here.
const SchemaVersion = 1

// CompilerVersion identifies the layout compiler that turns a resolved
// presentation into formatter parameters. Bump it when the compile mapping
// changes independently of declaration contents.
const CompilerVersion = 1

// SkinSchemaVersion identifies the semantic-token skin encoding.
const SkinSchemaVersion = 1

// ProfileNeutralV1 is the complete, independently runnable fallback profile.
const ProfileNeutralV1 = "neutral.v1"

// NeutralLineHeight is the xterm lineHeight used when density is unset or
// invalid. The frontend also treats values outside 1.0–1.5 as this default.
const NeutralLineHeight = 1.2

// State is a computed profile-level presentation state. Adapters never write
// this by hand; Resolve derives it from feature and dimension modes.
type State string

const (
	StateNeutral  State = "neutral"
	StatePartial  State = "partial"
	StateVerified State = "verified"
)

// FeatureMode is the adapter-declared mode for one catalog feature.
type FeatureMode string

const (
	FeatureModeNeutral       FeatureMode = "neutral"
	FeatureModeCustom        FeatureMode = "custom"
	FeatureModeNotApplicable FeatureMode = "not_applicable"
)

// FeatureState is the computed state of one feature after validation.
type FeatureState string

const (
	FeatureStateNeutral       FeatureState = "neutral"
	FeatureStatePartial       FeatureState = "partial"
	FeatureStateVerified      FeatureState = "verified"
	FeatureStateNotApplicable FeatureState = "not_applicable"
)

// DimensionID is a presentation evidence and fallback unit.
type DimensionID string

const (
	DimensionLayout    DimensionID = "layout"
	DimensionSkinDark  DimensionID = "skin.dark"
	DimensionSkinLight DimensionID = "skin.light"
	DimensionDensity   DimensionID = "density"
	DimensionFold      DimensionID = "fold"
)

// DimensionMode is the adapter-declared mode for one dimension.
type DimensionMode string

const (
	DimensionModeNeutral       DimensionMode = "neutral"
	DimensionModeCustom        DimensionMode = "custom"
	DimensionModeNotApplicable DimensionMode = "not_applicable"
)

// DimensionState is the computed per-dimension state. Dimensions are never
// partial: each one is neutral, verified, or not_applicable.
type DimensionState string

const (
	DimensionStateNeutral       DimensionState = "neutral"
	DimensionStateVerified      DimensionState = "verified"
	DimensionStateNotApplicable DimensionState = "not_applicable"
)

// EvidenceID is a machine-generated lock key (agent + logical capture + hash
// prefix). Slice B stores the field; Slice E closes it against the lock file.
type EvidenceID string

// ThemeVariant selects a resolved skin.
type ThemeVariant string

const (
	ThemeDark  ThemeVariant = "dark"
	ThemeLight ThemeVariant = "light"
)

// FoldDefaultState is the first-seen default for a fold key.
type FoldDefaultState string

const (
	FoldCollapsed FoldDefaultState = "collapsed"
	FoldExpanded  FoldDefaultState = "expanded"
)

// MigrationState records a compatibility-era production path. It is not a
// presentation state and is not counted in verified/partial coverage.
type MigrationState string

const (
	// MigrationLegacyUnverified means this Agent still uses the pre-declaration
	// production renderer (profileFor) and must not claim verified/partial.
	MigrationLegacyUnverified MigrationState = "legacy_unverified"
)

// Declaration is the adapter-owned static presentation contract for one Agent.
type Declaration struct {
	SchemaVersion     int
	AgentType         string
	ProfileID         string
	FallbackProfileID string
	Features          map[FeatureID]FeatureDeclaration
	ProfileDimensions map[DimensionID]DimensionDeclaration
}

// FeatureDeclaration is one catalog feature's declared mode and dimensions.
type FeatureDeclaration struct {
	Mode       FeatureMode
	Dimensions map[DimensionID]DimensionDeclaration
	ReasonCode string
}

// DimensionDeclaration is one evidence/fallback unit inside a feature or
// profile-level dimension map.
type DimensionDeclaration struct {
	Mode        DimensionMode
	Parameters  DimensionParameters
	EvidenceIDs []EvidenceID
	ReasonCode  string
}

// DimensionParameters is a typed union. Validate allows only the field that
// matches the DimensionID; declarations do not use map[string]any.
type DimensionParameters struct {
	Layout           *FeatureLayoutSpec
	Skin             *SkinSpec
	Fold             *FoldSpec
	FormatterDensity *FormatterDensitySpec
	TerminalDensity  *TerminalDensitySpec
}

// FeatureLayoutSpec selects a registered layout primitive and its parameters.
type FeatureLayoutSpec struct {
	Primitive  PrimitiveID
	Parameters PrimitiveParameters
}

// PrimitiveParameters is a typed union. Validate allows fields according to
// PrimitiveID; declarations do not use map[string]any.
type PrimitiveParameters struct {
	Box     *BoxSpec
	Marker  *MarkerSpec
	Labels  *LabelSpec
	Spacing *SpacingSpec
}

// BoxSpec is a shared box-drawing character set.
type BoxSpec struct {
	TL, TR, BL, BR string
	H, V           string
}

// MarkerSpec is a compact bullet or header glyph.
type MarkerSpec struct {
	Char string
}

// LabelSpec is a short header template. Values are literal labels, not format
// strings or Agent IDs.
type LabelSpec struct {
	UserHeader      string
	AssistantHeader bool
}

// SpacingSpec holds formatter spacing that changes ANSI columns or indent.
type SpacingSpec struct {
	ResultIndent string
}

// SkinSpec is an optional per-token RGB override. Nil or empty fields inherit
// the resolved neutral skin. Tokens are semantic, not raw ANSI or CSS.
type SkinSpec struct {
	Tool          *string
	Warning       *string
	Error         *string
	Success       *string
	Skill         *string
	Subagent      *string
	Muted         *string
	User          *string
	Fg            *string
	Bg            *string
	Banner        *string
	DiffDel       *string
	DiffAdd       *string
	SuccessBright *string
	ErrorBright   *string
}

// FoldSpec customizes fold chrome. Interaction (search, jump, copy) stays on
// the shared xterm matcher path and is not declared here.
type FoldSpec struct {
	HeaderPrimitive PrimitiveID
	ExpandedGlyph   string
	CollapsedGlyph  string
	DefaultState    FoldDefaultState
}

// FormatterDensitySpec holds spacing that changes ANSI rows or columns.
// Slice B has no custom formatter density; the type exists so later slices
// can add fields without a second union.
type FormatterDensitySpec struct{}

// TerminalDensitySpec holds xterm options that do not change formatter text.
type TerminalDensitySpec struct {
	LineHeight float64
}

// Resolved is the validator output: merged-from-neutral parameters, computed
// states, and content fingerprints. Resolve does not read sessions or images.
type Resolved struct {
	State              State
	ProfileID          string
	Features           map[FeatureID]ResolvedFeature
	Skins              map[ThemeVariant]ResolvedSkin
	ProfileDimensions  map[DimensionID]ResolvedDimension
	FormatterDensity   ResolvedFormatterDensity
	TerminalDensity    ResolvedTerminalDensity
	LayoutFingerprint  string
	SkinFingerprint    string
	FallbackProfileID  string
	FallbackReasonCode string
}

// ResolvedFeature is one catalog feature after dimension merge and state
// computation.
type ResolvedFeature struct {
	State      FeatureState
	Dimensions map[DimensionID]ResolvedDimension
	ReasonCode string
}

// ResolvedDimension is one dimension after merge. Custom parameters overlay
// the neutral baseline; unused union fields stay nil.
type ResolvedDimension struct {
	State      DimensionState
	Parameters DimensionParameters
	ReasonCode string
}

// ResolvedSkin is the effective semantic-token set for one theme variant.
// Empty strings inherit the client-side neutral token.
type ResolvedSkin struct {
	Tool          string
	Warning       string
	Error         string
	Success       string
	Skill         string
	Subagent      string
	Muted         string
	User          string
	Fg            string
	Bg            string
	Banner        string
	DiffDel       string
	DiffAdd       string
	SuccessBright string
	ErrorBright   string
}

// ResolvedFormatterDensity is the effective formatter spacing.
type ResolvedFormatterDensity struct{}

// ResolvedTerminalDensity is the effective xterm density.
type ResolvedTerminalDensity struct {
	LineHeight float64
}

// First-edition stable reason codes. User-visible copy is mapped by the
// frontend i18n catalog; APIs return only these machine keys.
const (
	ReasonEvidenceMissing      = "presentation_evidence_missing"
	ReasonFactMissing          = "presentation_fact_missing"
	ReasonFoldPairMissing      = "presentation_fold_pair_missing"
	ReasonThemeEvidenceMissing = "presentation_theme_evidence_missing"
	ReasonNotApplicable        = "presentation_not_applicable"
	ReasonDeclarationInvalid   = "presentation_declaration_invalid"
	ReasonUnknownAgent         = "presentation_unknown_agent"
	ReasonNonNativeSource      = "presentation_non_native_source"
)

// KnownReasonCodes returns the first-edition reason codes in stable order.
func KnownReasonCodes() []string {
	return []string{
		ReasonEvidenceMissing,
		ReasonFactMissing,
		ReasonFoldPairMissing,
		ReasonThemeEvidenceMissing,
		ReasonNotApplicable,
		ReasonDeclarationInvalid,
		ReasonUnknownAgent,
		ReasonNonNativeSource,
	}
}

// IsKnownReasonCode reports whether code is a first-edition reason code.
func IsKnownReasonCode(code string) bool {
	for _, known := range KnownReasonCodes() {
		if code == known {
			return true
		}
	}
	return false
}
