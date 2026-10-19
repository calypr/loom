// Command preview-covering-index-artifact recreates one captured Builder
// proposal with Loom's production authoring and dataframe compilers. It only
// writes the requested JSON artifact and never contacts Loom or ArangoDB.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/compilation"
)

type capturedReport struct {
	Project            string `json:"project"`
	Explorer           string `json:"explorer"`
	ExpectedGeneration string `json:"expectedGeneration"`
	Requests           []struct {
		Path     string          `json:"path"`
		Status   int             `json:"status"`
		Response json.RawMessage `json:"response"`
	} `json:"requests"`
	AuthoringRequests []capturedAuthoringRequest `json:"authoringRequests"`
}

type capturedAuthoringRequest struct {
	Pathname string          `json:"pathname"`
	Status   *int            `json:"status"`
	Body     json.RawMessage `json:"body"`
	Response json.RawMessage `json:"response"`
}

type capturedReconcileRequest struct {
	SnapshotToken string `json:"snapshotToken"`
	DraftVersion  int64  `json:"draftVersion"`
	DraftDigest   string `json:"draftDigest"`
}

type capturedCapabilityRequest struct {
	SnapshotToken        string `json:"snapshotToken"`
	ExpectedDraftVersion int64  `json:"expectedDraftVersion"`
	ExpectedDraftDigest  string `json:"expectedDraftDigest"`
	OutputID             string `json:"outputId"`
	StageID              string `json:"stageId"`
}

type proposalRequest struct {
	SnapshotToken         string                   `json:"snapshotToken"`
	Limit                 int                      `json:"limit"`
	OutputID              string                   `json:"outputId"`
	ChangedStepID         string                   `json:"changedStepId"`
	ExpectedDraftVersion  int64                    `json:"expectedDraftVersion"`
	ExpectedDraftDigest   string                   `json:"expectedDraftDigest"`
	CandidateConstruction authoringv2.Construction `json:"candidateConstruction"`
	PivotSources          []pivotSourceSelection   `json:"pivotSources"`
}

type pivotSourceSelection struct {
	ChoiceID string `json:"choiceId"`
	ColumnID string `json:"columnId"`
}

var finalReturnProjection = regexp.MustCompile(`\[@([A-Za-z0-9_]+)\]:\s*__loom_construction_final_row\.([A-Za-z0-9_]+)`)

type capturedChoice struct {
	ChoiceID     string `json:"choiceId"`
	FieldPath    string `json:"fieldPath"`
	FHIRType     string `json:"fhirType"`
	LogicalType  string `json:"logicalType"`
	Label        string `json:"label"`
	OccurrenceID string `json:"occurrenceId"`
}

type sourceInputResponse struct {
	SnapshotToken    string `json:"snapshotToken"`
	DraftVersion     int64  `json:"draftVersion"`
	DraftDigest      string `json:"draftDigest"`
	OutputID         string `json:"outputId"`
	StageID          string `json:"stageId"`
	PivotSourceInput struct {
		Choices []capturedChoice `json:"choices"`
	} `json:"pivotSourceInput"`
}

type artifact struct {
	Collection      string          `json:"collection"`
	Name            string          `json:"name"`
	Fields          []string        `json:"fields"`
	StoredValues    []string        `json:"storedValues"`
	Fingerprint     fingerprint     `json:"fingerprint"`
	Correspondence  candidateProof  `json:"correspondence"`
	QueryComparison queryComparison `json:"queryComparison"`
}

type productionSourceFingerprint struct {
	Verification  string                 `json:"verification,omitempty"`
	Algorithm     string                 `json:"algorithm"`
	Module        string                 `json:"module"`
	Package       string                 `json:"package"`
	GoVersion     string                 `json:"goVersion"`
	GOOS          string                 `json:"goos"`
	GOARCH        string                 `json:"goarch"`
	BuildSettings []buildSetting         `json:"buildSettings"`
	Packages      []string               `json:"packages"`
	Files         []productionSourceFile `json:"files"`
	FileCount     int                    `json:"fileCount"`
	SHA256        string                 `json:"sha256"`
}

type buildSetting struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type productionSourceFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type goListModule struct {
	Path string `json:"Path"`
	Main bool   `json:"Main"`
}

type goListPackage struct {
	ImportPath   string        `json:"ImportPath"`
	Module       *goListModule `json:"Module"`
	Dir          string        `json:"Dir"`
	Standard     bool          `json:"Standard"`
	GoFiles      []string      `json:"GoFiles"`
	CgoFiles     []string      `json:"CgoFiles"`
	CFiles       []string      `json:"CFiles"`
	CXXFiles     []string      `json:"CXXFiles"`
	HFiles       []string      `json:"HFiles"`
	FFiles       []string      `json:"FFiles"`
	SFiles       []string      `json:"SFiles"`
	SwigFiles    []string      `json:"SwigFiles"`
	SwigCXXFiles []string      `json:"SwigCXXFiles"`
	SysoFiles    []string      `json:"SysoFiles"`
	EmbedFiles   []string      `json:"EmbedFiles"`
}

type fingerprint struct {
	Algorithm                     string                      `json:"algorithm"`
	Mode                          string                      `json:"mode"`
	CapturedQueryFile             string                      `json:"capturedQueryFile"`
	CapturedQueryFileSHA256       string                      `json:"capturedQueryFileSha256"`
	CapturedAQLSHA256             string                      `json:"capturedAqlSha256"`
	CapturedBindVarsSHA256        string                      `json:"capturedBindVarsSha256"`
	RecompiledAQLSHA256           string                      `json:"recompiledAqlSha256"`
	RecompiledBindVarsSHA256      string                      `json:"recompiledBindVarsSha256"`
	RecompiledQueryFile           string                      `json:"recompiledQueryFile,omitempty"`
	RecompiledQueryFileSHA256     string                      `json:"recompiledQueryFileSha256,omitempty"`
	CapturedBaseReceiptID         string                      `json:"capturedBaseReceiptId"`
	CapturedBaseRecipeDigest      string                      `json:"capturedBaseRecipeDigest"`
	CandidateRequestSHA256        string                      `json:"candidateRequestSha256"`
	CandidateIsRequestOnly        bool                        `json:"candidateIsRequestOnly"`
	Project                       string                      `json:"project"`
	Generation                    string                      `json:"generation"`
	Explorer                      string                      `json:"explorer"`
	OutputID                      string                      `json:"outputId"`
	CandidateStepID               string                      `json:"candidateStepId"`
	DraftVersion                  int64                       `json:"draftVersion"`
	DraftDigest                   string                      `json:"draftDigest"`
	CapturedCatalogSnapshotToken  string                      `json:"capturedCatalogSnapshotToken"`
	CapturedCatalogSHA256         string                      `json:"capturedCatalogSha256"`
	ResolvedSchemaDigest          string                      `json:"resolvedSchemaDigest"`
	RecreatedCapabilitySnapshotID string                      `json:"recreatedCapabilitySnapshotId"`
	CompilerTranslationVersion    string                      `json:"compilerTranslationVersion"`
	CurrentProductionSource       productionSourceFingerprint `json:"currentProductionSource"`
}

type candidateProof struct {
	BaseOutputID                 string                `json:"baseOutputId"`
	CandidateOutputID            string                `json:"candidateOutputId"`
	CandidateStepID              string                `json:"candidateStepId"`
	SelectedSources              []selectedSourceProof `json:"selectedSources"`
	Pivot                        pivotProof            `json:"pivot"`
	CapturedPublicNames          []string              `json:"capturedPublicNames"`
	RecompiledPublic             []outputColumnProof   `json:"recompiledPublicColumns"`
	FinalSchema                  []outputColumnProof   `json:"finalOutputSchema"`
	SingleTerminalPivot          bool                  `json:"singleTerminalPivot"`
	ProjectionOnlySingleRootScan bool                  `json:"projectionOnlySingleRootScan"`
}

type selectedSourceProof struct {
	ColumnID       string `json:"columnId"`
	ChoiceIDSHA256 string `json:"choiceIdSha256"`
	OccurrenceID   string `json:"occurrenceId"`
	ResourceType   string `json:"resourceType"`
	FieldPath      string `json:"fieldPath"`
	FHIRType       string `json:"fhirType"`
	LogicalType    string `json:"logicalType"`
	Label          string `json:"label"`
}

type pivotProof struct {
	GroupKeyIDs            []string                                            `json:"groupKeyIds"`
	CategoryColumnID       string                                              `json:"categoryColumnId"`
	ValueColumnID          string                                              `json:"valueColumnId"`
	Categories             []authoringv2.ConstructionPivotCategory             `json:"categories"`
	DuplicatePolicy        authoringv2.ConstructionPivotDuplicatePolicy        `json:"duplicatePolicy"`
	MissingCellPolicy      authoringv2.ConstructionPivotMissingCellPolicy      `json:"missingCellPolicy"`
	UnlistedCategoryPolicy authoringv2.ConstructionPivotUnlistedCategoryPolicy `json:"unlistedCategoryPolicy"`
}

type outputColumnProof struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Label    string `json:"label"`
	Kind     string `json:"kind"`
	Nullable bool   `json:"nullable"`
	Internal bool   `json:"internal"`
	Identity bool   `json:"identity"`
}

type queryComparison struct {
	ExactAQLMatch         bool   `json:"exactAqlMatch"`
	ExactBindVarsMatch    bool   `json:"exactBindVarsMatch"`
	CapturedAQLSHA256     string `json:"capturedAqlSha256"`
	RecompiledAQLSHA256   string `json:"recompiledAqlSha256"`
	CapturedBindsSHA256   string `json:"capturedBindVarsSha256"`
	RecompiledBindsSHA256 string `json:"recompiledBindVarsSha256"`
	FirstDifference       string `json:"firstDifference"`
	FirstBindDifference   string `json:"firstBindDifference"`
}

type capturedQuery struct {
	Query    string         `json:"query"`
	BindVars map[string]any `json:"bindVars"`
}

type reconcileResponse struct {
	SnapshotToken            string                `json:"snapshotToken"`
	Generation               string                `json:"generation"`
	AuthorizationScopeDigest string                `json:"authorizationScopeDigest"`
	ResolvedSchemaDigest     string                `json:"resolvedSchemaDigest"`
	ReceiptID                string                `json:"receiptId"`
	RecipeDigest             string                `json:"recipeDigest"`
	ResolvedRecipeDigest     string                `json:"resolvedRecipeDigest"`
	Builder                  authoringv2.Workspace `json:"builder"`
}

type catalogEnvelope struct {
	Catalog      authoringv2.CatalogSnapshot `json:"catalog"`
	Workspace    authoringv2.Workspace       `json:"workspace"`
	DraftVersion int64                       `json:"draftVersion"`
	DraftDigest  string                      `json:"draftDigest"`
}

func main() {
	reportPath := flag.String("report", "/tmp/loom-root-quantity-cda-composite-streaming-native/report.json", "captured native Builder report JSON")
	queryPath := flag.String("query", "/tmp/loom-category-stream-query.json", "captured compiled AQL and bindVars JSON")
	outPath := flag.String("out", "/tmp/loom-category-pivot-covering-index-spec.json", "output compiler-owned spec JSON")
	compiledQueryOutPath := flag.String("compiled-query-out", "", "optional 0600 JSON output containing only the exact current recompiled query and bindVars")
	sourceManifestPath := flag.String("source-manifest", "", "pre-build source closure manifest from scripts/preview-covering-index-artifact.mjs")
	flag.Parse()
	if err := run(*reportPath, *queryPath, *outPath, *compiledQueryOutPath, *sourceManifestPath); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(reportPath, queryPath, outPath, compiledQueryOutPath, sourceManifestPath string) error {
	if compiledQueryOutPath != "" && (sameOutputPath(outPath, compiledQueryOutPath) ||
		sameOutputPath(queryPath, compiledQueryOutPath) || sameOutputPath(reportPath, compiledQueryOutPath)) {
		return errors.New("recompiled query output must be separate from the index spec and captured input files")
	}
	moduleRoot, err := productionModuleRoot()
	if err != nil {
		return err
	}
	if sourceManifestPath == "" {
		return errors.New("run through scripts/preview-covering-index-artifact.mjs so the source closure is captured before Go compiles this command")
	}
	manifestBytes, err := os.ReadFile(sourceManifestPath)
	if err != nil {
		return fmt.Errorf("read pre-build source closure manifest: %w", err)
	}
	var preBuildSource productionSourceFingerprint
	if err := json.Unmarshal(manifestBytes, &preBuildSource); err != nil {
		return fmt.Errorf("decode pre-build source closure manifest: %w", err)
	}
	productionSource, err := fingerprintProductionSources(moduleRoot, "./cmd/preview-covering-index-artifact")
	if err != nil {
		return fmt.Errorf("fingerprint current production source closure: %w", err)
	}
	if !sameSourceClosure(preBuildSource, productionSource) {
		return errors.New("go-run wrapper source manifest does not match the current production source closure")
	}
	productionSource.Verification = "go-run-wrapper-captured-source-closure-before-compilation-and-verified-after-start"
	reportBytes, err := os.ReadFile(reportPath)
	if err != nil {
		return fmt.Errorf("read captured report: %w", err)
	}
	queryBytes, err := os.ReadFile(queryPath)
	if err != nil {
		return fmt.Errorf("read captured query: %w", err)
	}
	var report capturedReport
	if err := json.Unmarshal(reportBytes, &report); err != nil {
		return fmt.Errorf("decode captured report: %w", err)
	}
	var expected capturedQuery
	if err := json.Unmarshal(queryBytes, &expected); err != nil {
		return fmt.Errorf("decode captured query: %w", err)
	}
	if strings.TrimSpace(expected.Query) == "" || expected.BindVars == nil {
		return errors.New("captured query must contain non-empty query and bindVars")
	}
	if report.Project == "" || report.Explorer == "" || report.ExpectedGeneration == "" {
		return errors.New("captured report lacks project, explorer, or expectedGeneration")
	}

	reconcileRequest, baseReceipt, err := findReconcile(report)
	if err != nil {
		return err
	}
	proposal, proposalBody, err := findProposal(report)
	if err != nil {
		return err
	}
	capabilityRequest, capabilityResponse, choices, err := findPivotSourceChoices(report, proposal)
	if err != nil {
		return err
	}
	capturedCatalog, err := findCatalogForProposal(report, proposal)
	if err != nil {
		return err
	}
	if err := validateCapturedScope(report, expected, reconcileRequest, baseReceipt, proposal, capabilityRequest, capabilityResponse, capturedCatalog); err != nil {
		return fmt.Errorf("captured input correlation: %w", err)
	}
	workspace, err := applyCapturedProposal(baseReceipt.Builder, proposal, choices)
	if err != nil {
		return err
	}
	snapshot, err := capabilitySnapshot(report.Project, capturedCatalog.Catalog)
	if err != nil {
		return err
	}
	compiled, err := compilation.CompileWorkspace(context.Background(), report.Project, report.Explorer, workspace, snapshot, compilation.ResolvedInputs{})
	if err != nil {
		return fmt.Errorf("recreate captured candidate through production authoring compiler: %w", err)
	}
	bindings := recipe.RuntimeBindings{
		Project: report.Project, DatasetGeneration: report.ExpectedGeneration,
		AuthScopeMode: authscope.ReadScopeUnrestricted, IncludeSourceIdentity: true,
	}
	plan, err := semantic.BuildRecipePlan(compiled.Bundle, bindings)
	if err != nil {
		return fmt.Errorf("build production semantic plan: %w", err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, capturedCatalog.Catalog.AuthorizationScopeDigest, report.ExpectedGeneration)
	if err != nil {
		return fmt.Errorf("resolve production semantic plan: %w", err)
	}
	limitValue, ok := expected.BindVars["limit"].(float64)
	if !ok || limitValue < 0 || limitValue != float64(int(limitValue)) {
		return errors.New("captured query limit bind is missing or invalid")
	}
	queries, err := compiler.CompileResolvedRecipePlanWithPolicy(resolved, int(limitValue), ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		return fmt.Errorf("compile production physical query: %w", err)
	}
	if len(queries) != 1 {
		return fmt.Errorf("captured candidate compiled to %d outputs, want one", len(queries))
	}
	actual := queries[0]
	if actual.PreviewCoveringIndex == nil {
		return errors.New("current production recompile has no PreviewCoveringIndexSpec")
	}
	baseReceiptID := baseReceipt.ReceiptID
	if baseReceiptID == "" || baseReceipt.RecipeDigest == "" {
		return errors.New("captured reconcile response lacks saved base receipt identity")
	}
	proof, err := proveCandidateCorrespondence(baseReceipt.Builder, proposal, proposalBody, choices, workspace, compiled.Bundle.Outputs, actual, expected)
	if err != nil {
		return fmt.Errorf("current recompile correspondence proof: %w", err)
	}
	if err := validateCurrentIndexSpec(*actual.PreviewCoveringIndex); err != nil {
		return fmt.Errorf("current compiler index spec: %w", err)
	}
	queryComparison := queryComparison{
		ExactAQLMatch: actual.Query == expected.Query, ExactBindVarsMatch: sameJSONValue(actual.BindVars, expected.BindVars),
		CapturedAQLSHA256: digest([]byte(expected.Query)), RecompiledAQLSHA256: digest([]byte(actual.Query)),
		CapturedBindsSHA256: digestJSON(expected.BindVars), RecompiledBindsSHA256: digestJSON(actual.BindVars),
		FirstDifference:     firstQueryDifference(expected.Query, actual.Query),
		FirstBindDifference: bindDifference(expected.BindVars, actual.BindVars),
	}
	var recompiledQueryBytes []byte
	recompiledQuerySHA256 := ""
	if compiledQueryOutPath != "" {
		recompiledQueryBytes, err = encodeRecompiledQueryArtifact(actual.Query, actual.BindVars)
		if err != nil {
			return fmt.Errorf("encode current recompiled query artifact: %w", err)
		}
		if err := verifyRecompiledQueryArtifact(recompiledQueryBytes, queryComparison.RecompiledAQLSHA256, queryComparison.RecompiledBindsSHA256); err != nil {
			return fmt.Errorf("verify current recompiled query artifact: %w", err)
		}
		recompiledQuerySHA256 = digest(recompiledQueryBytes)
	}
	identityBytes, _ := json.Marshal(snapshot.Identity)
	out := artifact{
		Collection: actual.PreviewCoveringIndex.Collection, Name: actual.PreviewCoveringIndex.Name,
		Fields: append([]string(nil), actual.PreviewCoveringIndex.Fields...), StoredValues: append([]string(nil), actual.PreviewCoveringIndex.StoredValues...),
		Correspondence: proof, QueryComparison: queryComparison,
		Fingerprint: fingerprint{
			Algorithm: "sha256", Mode: "verified-go-run-source-closure-current-production-recompile-from-captured-base-and-candidate-request",
			CapturedQueryFile: queryPath, CapturedQueryFileSHA256: digest(queryBytes),
			CapturedAQLSHA256: digest([]byte(expected.Query)), CapturedBindVarsSHA256: digestJSON(expected.BindVars),
			RecompiledAQLSHA256: digest([]byte(actual.Query)), RecompiledBindVarsSHA256: digestJSON(actual.BindVars),
			RecompiledQueryFile: compiledQueryOutPath, RecompiledQueryFileSHA256: recompiledQuerySHA256,
			CapturedBaseReceiptID: baseReceiptID, CapturedBaseRecipeDigest: baseReceipt.ResolvedRecipeDigest,
			CandidateRequestSHA256: digest(proposalBody), CandidateIsRequestOnly: true,
			Project: report.Project, Generation: report.ExpectedGeneration,
			Explorer: report.Explorer, OutputID: proposal.OutputID, CandidateStepID: proposal.ChangedStepID,
			DraftVersion: proposal.ExpectedDraftVersion, DraftDigest: proposal.ExpectedDraftDigest,
			CapturedCatalogSnapshotToken: capturedCatalog.Catalog.SnapshotToken,
			CapturedCatalogSHA256:        digestJSON(capturedCatalog.Catalog), ResolvedSchemaDigest: capturedCatalog.Catalog.ResolvedSchemaDigest,
			RecreatedCapabilitySnapshotID: digest(identityBytes), CompilerTranslationVersion: compiled.Bundle.TranslationVersion,
			CurrentProductionSource: productionSource,
		},
	}
	productionSourceAfter, err := fingerprintProductionSources(moduleRoot, "./cmd/preview-covering-index-artifact")
	if err != nil {
		return fmt.Errorf("recheck current production source closure: %w", err)
	}
	if productionSourceAfter.SHA256 != productionSource.SHA256 {
		return errors.New("production source closure changed during current recompile; rerun with a frozen source tree")
	}
	if compiledQueryOutPath != "" {
		if err := os.WriteFile(compiledQueryOutPath, recompiledQueryBytes, 0o600); err != nil {
			return fmt.Errorf("write current recompiled query artifact %q: %w", compiledQueryOutPath, err)
		}
		writtenQueryBytes, err := os.ReadFile(compiledQueryOutPath)
		if err != nil {
			return fmt.Errorf("reread current recompiled query artifact %q: %w", compiledQueryOutPath, err)
		}
		if digest(writtenQueryBytes) != out.Fingerprint.RecompiledQueryFileSHA256 {
			return errors.New("current recompiled query artifact changed after writing")
		}
		if err := verifyRecompiledQueryArtifactProof(writtenQueryBytes, out.Fingerprint); err != nil {
			return fmt.Errorf("verify written current recompiled query artifact: %w", err)
		}
	}
	encoded, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("encode index spec: %w", err)
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(outPath, encoded, 0o600); err != nil {
		return fmt.Errorf("write index spec: %w", err)
	}
	fmt.Printf("wrote %s\ncaptured query sha256: %s\nrecompiled query sha256: %s\nrecompiled bindVars sha256: %s\nindex: %s / %s\nstoredValues: %s\nproduction source sha256: %s (%d files)\n",
		outPath, out.Fingerprint.CapturedAQLSHA256, out.Fingerprint.RecompiledAQLSHA256, out.Fingerprint.RecompiledBindVarsSHA256,
		out.Collection, out.Name, strings.Join(out.StoredValues, ","), productionSource.SHA256, productionSource.FileCount)
	if compiledQueryOutPath != "" {
		fmt.Printf("recompiled query artifact: %s\nrecompiled query artifact sha256: %s\n", compiledQueryOutPath, out.Fingerprint.RecompiledQueryFileSHA256)
	}
	return nil
}

func sameOutputPath(left, right string) bool {
	leftAbs, leftErr := filepath.Abs(left)
	rightAbs, rightErr := filepath.Abs(right)
	return leftErr == nil && rightErr == nil && filepath.Clean(leftAbs) == filepath.Clean(rightAbs)
}

func encodeRecompiledQueryArtifact(query string, bindVars map[string]any) ([]byte, error) {
	if strings.TrimSpace(query) == "" || bindVars == nil {
		return nil, errors.New("current recompiled query and bindVars are required")
	}
	encoded, err := json.MarshalIndent(capturedQuery{Query: query, BindVars: bindVars}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

func verifyRecompiledQueryArtifact(data []byte, expectedQuerySHA256, expectedBindVarsSHA256 string) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var compiled capturedQuery
	if err := decoder.Decode(&compiled); err != nil {
		return fmt.Errorf("decode exact query and bindVars: %w", err)
	}
	if strings.TrimSpace(compiled.Query) == "" || compiled.BindVars == nil {
		return errors.New("query artifact must contain non-empty query and bindVars")
	}
	if digest([]byte(compiled.Query)) != expectedQuerySHA256 {
		return errors.New("query artifact AQL hash differs from the current recompile")
	}
	if digestJSON(compiled.BindVars) != expectedBindVarsSHA256 {
		return errors.New("query artifact bindVars hash differs from the current recompile")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("query artifact contains trailing data")
	}
	return nil
}

func verifyRecompiledQueryArtifactProof(data []byte, proof fingerprint) error {
	if proof.RecompiledQueryFileSHA256 == "" || digest(data) != proof.RecompiledQueryFileSHA256 {
		return errors.New("query artifact file hash differs from the proof fingerprint")
	}
	return verifyRecompiledQueryArtifact(data, proof.RecompiledAQLSHA256, proof.RecompiledBindVarsSHA256)
}

func productionModuleRoot() (string, error) {
	_, sourcePath, _, ok := runtime.Caller(0)
	if !ok || sourcePath == "" {
		return "", errors.New("cannot locate producer source to fingerprint its module")
	}
	candidates := []string{sourcePath}
	if !filepath.IsAbs(sourcePath) {
		workingDirectory, err := os.Getwd()
		if err == nil {
			candidates = append(candidates, filepath.Join(workingDirectory, sourcePath))
		}
	}
	workingDirectory, err := os.Getwd()
	if err == nil {
		candidates = append(candidates, workingDirectory)
	}
	for _, candidate := range candidates {
		current, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		if info, err := os.Stat(current); err == nil && !info.IsDir() {
			current = filepath.Dir(current)
		}
		for {
			if _, err := os.Stat(filepath.Join(current, "go.mod")); err == nil {
				return filepath.EvalSymlinks(current)
			}
			parent := filepath.Dir(current)
			if parent == current {
				break
			}
			current = parent
		}
	}
	return "", errors.New("cannot find go.mod from producer source or working directory")
}

func fingerprintProductionSources(moduleRoot, packagePattern string) (productionSourceFingerprint, error) {
	root, err := filepath.EvalSymlinks(moduleRoot)
	if err != nil {
		return productionSourceFingerprint{}, fmt.Errorf("resolve module root: %w", err)
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return productionSourceFingerprint{}, fmt.Errorf("make module root absolute: %w", err)
	}
	command := exec.Command("go", "list", "-deps", "-json", packagePattern)
	command.Dir = root
	listOutput, err := command.Output()
	if err != nil {
		return productionSourceFingerprint{}, fmt.Errorf("go list production source closure: %w: %s", err, strings.TrimSpace(string(listOutput)))
	}
	decoder := json.NewDecoder(bytes.NewReader(listOutput))
	packageSet := map[string]struct{}{}
	fileSet := map[string]string{}
	modulePath := ""
	for {
		var item goListPackage
		if err := decoder.Decode(&item); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return productionSourceFingerprint{}, fmt.Errorf("decode go list package: %w", err)
		}
		if item.Dir == "" || item.ImportPath == "" {
			continue
		}
		packageDir, err := filepath.EvalSymlinks(item.Dir)
		if err != nil {
			return productionSourceFingerprint{}, fmt.Errorf("resolve package %s: %w", item.ImportPath, err)
		}
		packageDir, err = filepath.Abs(packageDir)
		if err != nil {
			return productionSourceFingerprint{}, err
		}
		packageRelative, err := filepath.Rel(root, packageDir)
		if err != nil || packageRelative == ".." || strings.HasPrefix(packageRelative, ".."+string(filepath.Separator)) {
			continue
		}
		packageSet[item.ImportPath] = struct{}{}
		if item.Module != nil && item.Module.Main {
			modulePath = item.Module.Path
		}
		fileNames := append([]string{}, item.GoFiles...)
		fileNames = append(fileNames, item.CgoFiles...)
		fileNames = append(fileNames, item.CFiles...)
		fileNames = append(fileNames, item.CXXFiles...)
		fileNames = append(fileNames, item.HFiles...)
		fileNames = append(fileNames, item.FFiles...)
		fileNames = append(fileNames, item.SFiles...)
		fileNames = append(fileNames, item.SwigFiles...)
		fileNames = append(fileNames, item.SwigCXXFiles...)
		fileNames = append(fileNames, item.SysoFiles...)
		fileNames = append(fileNames, item.EmbedFiles...)
		for _, fileName := range fileNames {
			filePath := filepath.Join(packageDir, fileName)
			resolvedFile, err := filepath.EvalSymlinks(filePath)
			if err != nil {
				return productionSourceFingerprint{}, fmt.Errorf("resolve production source %s: %w", fileName, err)
			}
			resolvedFile, err = filepath.Abs(resolvedFile)
			if err != nil {
				return productionSourceFingerprint{}, err
			}
			relative, err := filepath.Rel(root, resolvedFile)
			if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				return productionSourceFingerprint{}, fmt.Errorf("production source %s resolves outside module root", fileName)
			}
			fileSet[filepath.ToSlash(relative)] = resolvedFile
		}
	}
	if len(packageSet) == 0 || modulePath == "" {
		return productionSourceFingerprint{}, errors.New("go list returned no in-module production packages or module identity")
	}
	for _, moduleFile := range []string{"go.mod", "go.sum", "go.work", "go.work.sum", "vendor/modules.txt", "scripts/preview-covering-index-artifact.mjs"} {
		full := filepath.Join(root, moduleFile)
		if _, err := os.Stat(full); err == nil {
			resolved, err := filepath.EvalSymlinks(full)
			if err != nil {
				return productionSourceFingerprint{}, fmt.Errorf("resolve source manifest file %s: %w", moduleFile, err)
			}
			fileSet[filepath.ToSlash(moduleFile)] = resolved
		}
	}
	files := make([]productionSourceFile, 0, len(fileSet))
	for relative, full := range fileSet {
		content, err := os.ReadFile(full)
		if err != nil {
			return productionSourceFingerprint{}, fmt.Errorf("read production source %s: %w", relative, err)
		}
		files = append(files, productionSourceFile{Path: relative, SHA256: digest(content)})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	packages := make([]string, 0, len(packageSet))
	for importPath := range packageSet {
		packages = append(packages, importPath)
	}
	sort.Strings(packages)
	settings := []buildSetting{}
	goVersion := runtime.Version()
	if info, ok := debug.ReadBuildInfo(); ok {
		if info.GoVersion != "" {
			goVersion = info.GoVersion
		}
		for _, setting := range info.Settings {
			settings = append(settings, buildSetting{Key: setting.Key, Value: setting.Value})
		}
	}
	sort.Slice(settings, func(i, j int) bool { return settings[i].Key < settings[j].Key })
	fingerprint := productionSourceFingerprint{
		Algorithm: "sha256", Module: modulePath, Package: packagePattern, GoVersion: goVersion,
		GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, BuildSettings: settings,
		Packages: packages, Files: files, FileCount: len(files),
	}
	var canonical bytes.Buffer
	fmt.Fprintf(&canonical, "module:%s\npackage:%s\n", fingerprint.Module, fingerprint.Package)
	for _, importPath := range packages {
		fmt.Fprintf(&canonical, "package:%s\n", importPath)
	}
	for _, file := range files {
		fmt.Fprintf(&canonical, "file:%s:%s\n", file.Path, file.SHA256)
	}
	fingerprint.SHA256 = digest(canonical.Bytes())
	return fingerprint, nil
}

func sameSourceClosure(left, right productionSourceFingerprint) bool {
	if left.Algorithm != right.Algorithm || left.Module != right.Module || left.Package != right.Package ||
		left.SHA256 != right.SHA256 || left.FileCount != right.FileCount || len(left.Packages) != len(right.Packages) || len(left.Files) != len(right.Files) {
		return false
	}
	for index := range left.Packages {
		if left.Packages[index] != right.Packages[index] {
			return false
		}
	}
	for index := range left.Files {
		if left.Files[index] != right.Files[index] {
			return false
		}
	}
	return true
}

func firstQueryDifference(left, right string) string {
	leftLines, rightLines := strings.Split(left, "\n"), strings.Split(right, "\n")
	limit := len(leftLines)
	if len(rightLines) < limit {
		limit = len(rightLines)
	}
	for index := 0; index < limit; index++ {
		if leftLines[index] != rightLines[index] {
			return fmt.Sprintf("line %d captured=%q recreated=%q", index+1, leftLines[index], rightLines[index])
		}
	}
	if len(leftLines) != len(rightLines) {
		return fmt.Sprintf("line count captured=%d recreated=%d", len(leftLines), len(rightLines))
	}
	return "none"
}

func bindDifference(left, right map[string]any) string {
	keys := map[string]bool{}
	for key := range left {
		keys[key] = true
	}
	for key := range right {
		keys[key] = true
	}
	ordered := make([]string, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	for _, key := range ordered {
		leftValue, leftOK := left[key]
		rightValue, rightOK := right[key]
		if !leftOK || !rightOK || !sameJSONValue(leftValue, rightValue) {
			return fmt.Sprintf("%s captured=%v recreated=%v capturedPresent=%t recreatedPresent=%t", key, leftValue, rightValue, leftOK, rightOK)
		}
	}
	return "none"
}

func authoringPath(report capturedReport, endpoint string) string {
	return "/api/v1/projects/" + url.PathEscape(report.Project) + "/explorers/" +
		url.PathEscape(report.Explorer) + "/authoring/v2/" + endpoint
}

func findReconcile(report capturedReport) (capturedReconcileRequest, reconcileResponse, error) {
	path := authoringPath(report, "reconcile")
	matches := make([]capturedAuthoringRequest, 0, 1)
	for _, request := range report.AuthoringRequests {
		if request.Pathname == path {
			matches = append(matches, request)
		}
	}
	if len(matches) != 1 || matches[0].Status == nil || *matches[0].Status != 200 || len(matches[0].Response) == 0 {
		return capturedReconcileRequest{}, reconcileResponse{}, fmt.Errorf("report has %d saved-base reconcile events at %s; want exactly one successful response", len(matches), path)
	}
	var request capturedReconcileRequest
	if err := json.Unmarshal(matches[0].Body, &request); err != nil {
		return capturedReconcileRequest{}, reconcileResponse{}, fmt.Errorf("decode saved base reconcile request: %w", err)
	}
	var response reconcileResponse
	if err := json.Unmarshal(matches[0].Response, &response); err != nil {
		return capturedReconcileRequest{}, reconcileResponse{}, fmt.Errorf("decode saved base reconcile response: %w", err)
	}
	return request, response, nil
}

func findProposal(report capturedReport) (proposalRequest, json.RawMessage, error) {
	path := authoringPath(report, "construction-proposals")
	matches := make([]capturedAuthoringRequest, 0, 1)
	for _, request := range report.AuthoringRequests {
		if request.Pathname == path {
			matches = append(matches, request)
		}
	}
	if len(matches) != 1 {
		return proposalRequest{}, nil, fmt.Errorf("report has %d construction proposal requests at %s; want exactly one", len(matches), path)
	}
	request := matches[0]
	var proposal proposalRequest
	if err := json.Unmarshal(request.Body, &proposal); err != nil {
		return proposalRequest{}, nil, fmt.Errorf("decode captured candidate request: %w", err)
	}
	if proposal.OutputID == "" || proposal.ChangedStepID == "" || proposal.SnapshotToken == "" {
		return proposalRequest{}, nil, errors.New("captured candidate request lacks output, changed step, or catalog snapshot token")
	}
	return proposal, append(json.RawMessage(nil), request.Body...), nil
}

func findPivotSourceChoices(report capturedReport, proposal proposalRequest) (capturedCapabilityRequest, sourceInputResponse, map[string]capturedChoice, error) {
	path := authoringPath(report, "construction-capabilities")
	var selectedRequest capturedCapabilityRequest
	var selectedResponse sourceInputResponse
	matches := 0
	for _, request := range report.AuthoringRequests {
		if request.Pathname != path {
			continue
		}
		var candidateRequest capturedCapabilityRequest
		if err := json.Unmarshal(request.Body, &candidateRequest); err != nil {
			return capturedCapabilityRequest{}, sourceInputResponse{}, nil, fmt.Errorf("decode captured capability request: %w", err)
		}
		if candidateRequest.OutputID != proposal.OutputID || candidateRequest.SnapshotToken != proposal.SnapshotToken ||
			candidateRequest.ExpectedDraftVersion != proposal.ExpectedDraftVersion || candidateRequest.ExpectedDraftDigest != proposal.ExpectedDraftDigest ||
			candidateRequest.StageID != "source_projection" {
			continue
		}
		matches++
		if request.Status == nil || *request.Status != 200 || len(request.Response) == 0 {
			return capturedCapabilityRequest{}, sourceInputResponse{}, nil, errors.New("candidate-bound Pivot capability request has no successful response")
		}
		var candidateResponse sourceInputResponse
		if err := json.Unmarshal(request.Response, &candidateResponse); err != nil {
			return capturedCapabilityRequest{}, sourceInputResponse{}, nil, fmt.Errorf("decode captured Pivot source choices: %w", err)
		}
		selectedRequest, selectedResponse = candidateRequest, candidateResponse
	}
	if matches != 1 {
		return capturedCapabilityRequest{}, sourceInputResponse{}, nil, fmt.Errorf("found %d Pivot capability responses bound to candidate output/draft/snapshot; want exactly one", matches)
	}
	choices := make(map[string]capturedChoice, len(selectedResponse.PivotSourceInput.Choices))
	for _, choice := range selectedResponse.PivotSourceInput.Choices {
		if choice.ChoiceID == "" {
			return capturedCapabilityRequest{}, sourceInputResponse{}, nil, errors.New("captured Pivot source choice has an empty signed choice ID")
		}
		if _, duplicate := choices[choice.ChoiceID]; duplicate {
			return capturedCapabilityRequest{}, sourceInputResponse{}, nil, fmt.Errorf("captured Pivot source choices duplicate choice ID %q", choice.ChoiceID)
		}
		choices[choice.ChoiceID] = choice
	}
	return selectedRequest, selectedResponse, choices, nil
}

func findCatalogForProposal(report capturedReport, proposal proposalRequest) (catalogEnvelope, error) {
	path := authoringPath(report, "builder")
	var selected catalogEnvelope
	var selectedCatalogDigest string
	matches := 0
	for _, request := range report.Requests {
		if request.Path != path || request.Status != 200 {
			continue
		}
		var envelope catalogEnvelope
		publicResponse, err := withoutCapturedChoiceInterfaces(request.Response)
		if err != nil {
			return catalogEnvelope{}, fmt.Errorf("sanitize captured catalog choice payloads: %w", err)
		}
		if err := json.Unmarshal(publicResponse, &envelope); err != nil {
			return catalogEnvelope{}, fmt.Errorf("decode captured public builder/catalog snapshot: %w", err)
		}
		if envelope.Catalog.SnapshotToken != proposal.SnapshotToken || envelope.Catalog.SourceGeneration != report.ExpectedGeneration {
			continue
		}
		if envelope.DraftVersion == proposal.ExpectedDraftVersion && envelope.DraftDigest != proposal.ExpectedDraftDigest {
			return catalogEnvelope{}, errors.New("builder catalog at the candidate draft version has a conflicting draft digest")
		}
		if envelope.DraftVersion != proposal.ExpectedDraftVersion || envelope.DraftDigest != proposal.ExpectedDraftDigest {
			continue
		}
		if len(envelope.Catalog.Candidates) == 0 {
			return catalogEnvelope{}, errors.New("captured public catalog snapshot has no candidates")
		}
		catalogDigest := digestJSON(envelope.Catalog)
		if matches > 0 {
			if catalogDigest != selectedCatalogDigest {
				return catalogEnvelope{}, errors.New("same captured snapshot token maps to conflicting public catalog payloads")
			}
			return catalogEnvelope{}, errors.New("report has multiple public catalogs bound to the same candidate draft and snapshot; ambiguous source capture")
		}
		selected, selectedCatalogDigest = envelope, catalogDigest
		matches++
	}
	if matches != 1 {
		return catalogEnvelope{}, fmt.Errorf("report has %d public builder/catalog snapshots bound to candidate draft and token %q; want exactly one", matches, proposal.SnapshotToken)
	}
	return selected, nil
}

func validateCapturedScope(report capturedReport, captured capturedQuery, reconcileRequest capturedReconcileRequest, reconcile reconcileResponse,
	proposal proposalRequest, capabilityRequest capturedCapabilityRequest, capabilityResponse sourceInputResponse, catalog catalogEnvelope) error {
	if report.Project == "" || report.Explorer == "" || report.ExpectedGeneration == "" {
		return errors.New("report scope is incomplete")
	}
	baseDigest, err := reconcile.Builder.Digest()
	if err != nil {
		return fmt.Errorf("digest saved base receipt workspace: %w", err)
	}
	if reconcileRequest.SnapshotToken == "" || reconcileRequest.SnapshotToken != proposal.SnapshotToken || reconcile.SnapshotToken != reconcileRequest.SnapshotToken {
		return errors.New("reconcile, proposal, and capability snapshot tokens do not identify the same catalog snapshot")
	}
	if reconcileRequest.DraftVersion <= 0 || reconcileRequest.DraftVersion != proposal.ExpectedDraftVersion ||
		reconcileRequest.DraftDigest == "" || reconcileRequest.DraftDigest != proposal.ExpectedDraftDigest || baseDigest != proposal.ExpectedDraftDigest {
		return fmt.Errorf("proposal draft identity does not match saved base reconcile workspace: reconcile=%d/%s workspace=%s candidate=%d/%s",
			reconcileRequest.DraftVersion, reconcileRequest.DraftDigest, baseDigest, proposal.ExpectedDraftVersion, proposal.ExpectedDraftDigest)
	}
	if reconcile.ReceiptID == "" || reconcile.RecipeDigest == "" || reconcile.Generation != report.ExpectedGeneration {
		return errors.New("saved base receipt identity or generation does not match report scope")
	}
	if capabilityRequest.OutputID != proposal.OutputID || capabilityRequest.StageID != "source_projection" ||
		capabilityRequest.SnapshotToken != proposal.SnapshotToken || capabilityRequest.ExpectedDraftVersion != proposal.ExpectedDraftVersion ||
		capabilityRequest.ExpectedDraftDigest != proposal.ExpectedDraftDigest {
		return errors.New("captured Pivot capabilities request is not bound to the proposal output, snapshot, and base draft")
	}
	if capabilityResponse.OutputID != capabilityRequest.OutputID || capabilityResponse.StageID != capabilityRequest.StageID ||
		capabilityResponse.SnapshotToken != capabilityRequest.SnapshotToken || capabilityResponse.DraftVersion != capabilityRequest.ExpectedDraftVersion ||
		capabilityResponse.DraftDigest != capabilityRequest.ExpectedDraftDigest {
		return errors.New("captured Pivot capabilities response identity does not match its request")
	}
	if catalog.Catalog.SnapshotToken != proposal.SnapshotToken || catalog.Catalog.SourceGeneration != report.ExpectedGeneration ||
		catalog.DraftVersion != proposal.ExpectedDraftVersion || catalog.DraftDigest != proposal.ExpectedDraftDigest {
		return errors.New("captured public catalog does not match report generation and proposal base identity")
	}
	if catalog.Catalog.AuthorizationScopeDigest != reconcile.AuthorizationScopeDigest {
		return errors.New("captured catalog authorization scope differs from saved base receipt")
	}
	if queryProject, ok := captured.BindVars["project"].(string); !ok || queryProject != report.Project {
		return fmt.Errorf("captured query project bind %v does not match report project %q", captured.BindVars["project"], report.Project)
	}
	if queryGeneration, ok := captured.BindVars["dataset_generation"].(string); !ok || queryGeneration != report.ExpectedGeneration {
		return fmt.Errorf("captured query generation bind %v does not match report generation %q", captured.BindVars["dataset_generation"], report.ExpectedGeneration)
	}
	if collection, ok := captured.BindVars["@root_collection"].(string); !ok || collection != "Observation" {
		return fmt.Errorf("captured query root collection %v is not Observation", captured.BindVars["@root_collection"])
	}
	limit, ok := captured.BindVars["limit"].(float64)
	if !ok || limit < 0 || limit != float64(int(limit)) || proposal.Limit != int(limit) {
		return fmt.Errorf("candidate limit %d does not match captured query limit bind %v", proposal.Limit, captured.BindVars["limit"])
	}
	return nil
}

// The captured catalog includes signed ConstructionChoice values whose Go
// domain representation is an interface. They are not needed to recreate a
// compile: CompileWorkspace issues fresh compiler choices from the mapped
// capability snapshot. Strip only that opaque field before decoding the
// public catalog facts.
func withoutCapturedChoiceInterfaces(raw json.RawMessage) ([]byte, error) {
	var response map[string]json.RawMessage
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, err
	}
	var catalog map[string]json.RawMessage
	if err := json.Unmarshal(response["catalog"], &catalog); err != nil {
		return nil, err
	}
	var candidates []map[string]json.RawMessage
	if err := json.Unmarshal(catalog["candidates"], &candidates); err != nil {
		return nil, err
	}
	for _, candidate := range candidates {
		delete(candidate, "constructionChoice")
	}
	candidateBytes, err := json.Marshal(candidates)
	if err != nil {
		return nil, err
	}
	catalog["candidates"] = candidateBytes
	catalogBytes, err := json.Marshal(catalog)
	if err != nil {
		return nil, err
	}
	response["catalog"] = catalogBytes
	return json.Marshal(response)
}

func applyCapturedProposal(workspace authoringv2.Workspace, proposal proposalRequest, choices map[string]capturedChoice) (authoringv2.Workspace, error) {
	if proposal.CandidateConstruction.Version == 0 || len(proposal.CandidateConstruction.Steps) == 0 {
		return authoringv2.Workspace{}, errors.New("captured proposal has no candidate construction")
	}
	documentIndex := -1
	for index := range workspace.Documents {
		if workspace.Documents[index].Output.ID == proposal.OutputID {
			documentIndex = index
			break
		}
	}
	if documentIndex < 0 {
		return authoringv2.Workspace{}, fmt.Errorf("captured proposal output %q is absent from saved base receipt workspace", proposal.OutputID)
	}
	baseDocument := workspace.Documents[documentIndex]
	upgraded, err := authoringv2.UpgradeDocumentToConstruction(baseDocument)
	if err != nil {
		return authoringv2.Workspace{}, fmt.Errorf("apply production first-edit construction migration: %w", err)
	}
	construction := proposal.CandidateConstruction
	construction.SourceProjections = append([]authoringv2.ConstructionSourceProjection(nil), upgraded.Construction.SourceProjections...)
	for _, selection := range proposal.PivotSources {
		choice, ok := choices[selection.ChoiceID]
		if !ok || choice.FieldPath == "" || choice.LogicalType == "" || choice.FHIRType == "" {
			return authoringv2.Workspace{}, fmt.Errorf("captured pivot source %q does not resolve to a compiler choice", selection.ChoiceID)
		}
		construction.SourceProjections = append(construction.SourceProjections, authoringv2.ConstructionSourceProjection{
			ColumnID: selection.ColumnID, OwnerStepID: proposal.ChangedStepID, OccurrenceID: choice.OccurrenceID,
			FieldPath: choice.FieldPath, FHIRType: choice.FHIRType, LogicalType: choice.LogicalType, Label: choice.Label,
		})
	}
	sort.Slice(construction.SourceProjections, func(i, j int) bool {
		return construction.SourceProjections[i].ColumnID < construction.SourceProjections[j].ColumnID
	})
	candidateDocument, _, err := upgraded.AnalyzeConstructionCandidate(construction, proposal.ChangedStepID, nil)
	if err != nil {
		return authoringv2.Workspace{}, fmt.Errorf("apply production construction candidate analysis: %w", err)
	}
	workspace.Documents[documentIndex] = candidateDocument
	return workspace, nil
}

func proveCandidateCorrespondence(base authoringv2.Workspace, proposal proposalRequest, proposalBody json.RawMessage,
	choices map[string]capturedChoice, recreated authoringv2.Workspace, outputs []recipe.Output,
	compiled compiler.CompiledQuery, captured capturedQuery) (candidateProof, error) {
	baseDocument, ok := workspaceDocumentByOutputID(base, proposal.OutputID)
	if !ok {
		return candidateProof{}, fmt.Errorf("saved base receipt has no output %q", proposal.OutputID)
	}
	document, ok := workspaceDocumentByOutputID(recreated, proposal.OutputID)
	if !ok {
		return candidateProof{}, fmt.Errorf("recreated workspace has no output %q", proposal.OutputID)
	}
	if baseDocument.RootResourceType != "Observation" || baseDocument.Route.OccurrenceID != authoringv2.RootOccurrenceID ||
		document.RootResourceType != baseDocument.RootResourceType || document.Route.OccurrenceID != baseDocument.Route.OccurrenceID {
		return candidateProof{}, errors.New("saved base and candidate do not retain the Observation root occurrence")
	}
	if proposal.OutputID == "" || proposal.ChangedStepID == "" || proposal.ExpectedDraftVersion <= 0 || proposal.ExpectedDraftDigest == "" || len(proposalBody) == 0 {
		return candidateProof{}, errors.New("captured candidate request lacks output, changed step, draft identity, or raw request bytes")
	}
	construction := document.Construction
	if construction == nil || len(construction.Steps) != 1 || construction.Steps[0].ID != proposal.ChangedStepID ||
		construction.Steps[0].Operation.Kind != authoringv2.ConstructionOperationPivot || construction.Steps[0].Operation.Pivot == nil {
		return candidateProof{}, errors.New("candidate is not exactly one terminal Pivot construction")
	}
	step := construction.Steps[0]
	if len(step.Inputs) != 1 || step.Inputs[0].Kind != authoringv2.ConstructionInputSourceProjection {
		return candidateProof{}, errors.New("Pivot input is not the captured source projection stage")
	}
	if len(proposal.PivotSources) != 3 {
		return candidateProof{}, fmt.Errorf("captured Pivot has %d signed source selections, want status/code/value", len(proposal.PivotSources))
	}

	wantSources := map[string]struct {
		fhirType    string
		logicalType string
	}{
		"status":              {fhirType: "string", logicalType: "string"},
		"valueQuantity.code":  {fhirType: "string", logicalType: "string"},
		"valueQuantity.value": {fhirType: "decimal", logicalType: "decimal"},
	}
	selectedByPath := make(map[string]pivotSourceSelection, len(proposal.PivotSources))
	selectedProof := make([]selectedSourceProof, 0, len(proposal.PivotSources))
	for _, selection := range proposal.PivotSources {
		choice, exists := choices[selection.ChoiceID]
		if !exists || choice.ChoiceID != selection.ChoiceID || choice.OccurrenceID != authoringv2.RootOccurrenceID {
			return candidateProof{}, fmt.Errorf("selected source %q is not a captured signed root-occurrence choice", selection.ColumnID)
		}
		want, expectedPath := wantSources[choice.FieldPath]
		if !expectedPath || choice.FHIRType != want.fhirType || choice.LogicalType != want.logicalType || choice.Label == "" {
			return candidateProof{}, fmt.Errorf("selected choice path/type %q/%s/%s is outside the captured Pivot source contract", choice.FieldPath, choice.FHIRType, choice.LogicalType)
		}
		if _, duplicate := selectedByPath[choice.FieldPath]; duplicate || selection.ColumnID == "" {
			return candidateProof{}, fmt.Errorf("duplicate path or empty stable column ID in selected Pivot source %q", choice.FieldPath)
		}
		selectedByPath[choice.FieldPath] = selection
		selectedProof = append(selectedProof, selectedSourceProof{
			ColumnID: selection.ColumnID, ChoiceIDSHA256: digest([]byte(selection.ChoiceID)),
			OccurrenceID: choice.OccurrenceID, ResourceType: document.RootResourceType, FieldPath: choice.FieldPath,
			FHIRType: choice.FHIRType, LogicalType: choice.LogicalType, Label: choice.Label,
		})
	}
	if len(selectedByPath) != len(wantSources) {
		return candidateProof{}, fmt.Errorf("selected root paths are %v, want status, valueQuantity.code, valueQuantity.value", sortedSourcePaths(selectedByPath))
	}
	sort.Slice(selectedProof, func(i, j int) bool { return selectedProof[i].FieldPath < selectedProof[j].FieldPath })
	statusID := selectedByPath["status"].ColumnID
	categoryID := selectedByPath["valueQuantity.code"].ColumnID
	valueID := selectedByPath["valueQuantity.value"].ColumnID
	pivot := step.Operation.Pivot
	if len(pivot.GroupKeyIDs) != 1 || pivot.GroupKeyIDs[0] != statusID || pivot.CategoryColumnID != categoryID || pivot.ValueColumnID != valueID {
		return candidateProof{}, fmt.Errorf("Pivot input IDs do not match signed source selections: group=%v category=%q value=%q", pivot.GroupKeyIDs, pivot.CategoryColumnID, pivot.ValueColumnID)
	}
	if pivot.DuplicatePolicy != authoringv2.ConstructionPivotDuplicateError ||
		pivot.MissingCellPolicy != authoringv2.ConstructionPivotMissingNull ||
		pivot.UnlistedCategoryPolicy != authoringv2.ConstructionPivotUnlistedError {
		return candidateProof{}, fmt.Errorf("Pivot policies changed: duplicate=%s missing=%s unlisted=%s", pivot.DuplicatePolicy, pivot.MissingCellPolicy, pivot.UnlistedCategoryPolicy)
	}
	if len(pivot.Categories) != 2 || !isMissingCategory(pivot.Categories[0].Key) || !isStringCategory(pivot.Categories[1].Key, "d") {
		return candidateProof{}, errors.New("Pivot categories are not exactly missing and string d in captured order")
	}
	categoryOutputIDs := []string{pivot.Categories[0].OutputColumnID, pivot.Categories[1].OutputColumnID}
	declaredByID := make(map[string]authoringv2.StageColumn, len(step.Outputs))
	for _, output := range step.Outputs {
		declaredByID[output.ID] = output
	}
	wantDeclared := []struct{ id, name string }{
		{statusID, "status"}, {categoryOutputIDs[0], "missing_value"}, {categoryOutputIDs[1], "d"},
	}
	for _, want := range wantDeclared {
		output, exists := declaredByID[want.id]
		if !exists || output.Name != want.name || output.ID == "" || strings.Contains(output.Name, "__construction_source_") {
			return candidateProof{}, fmt.Errorf("candidate output declaration for %q = %#v, want public name %q", want.id, output, want.name)
		}
	}
	statusOutput := declaredByID[statusID]
	statusSelection := selectedByPath["status"]
	statusChoice := choices[statusSelection.ChoiceID]
	if statusOutput.Label != statusChoice.Label ||
		statusOutput.Type != wantSources["status"].logicalType {
		return candidateProof{}, fmt.Errorf("group output %q does not preserve declared label and current signed-source type: %#v", statusID, statusOutput)
	}
	if len(outputs) != 1 || outputs[0].Name != proposal.OutputID || outputs[0].Construction == nil || len(outputs[0].Construction.Steps) != 1 {
		return candidateProof{}, errors.New("production authoring compiler did not produce exactly one output with one Pivot")
	}
	bundleStep := outputs[0].Construction.Steps[0]
	if bundleStep.Operation.Kind != recipe.ConstructionPivotOp || bundleStep.Operation.Pivot == nil ||
		!samePivotContract(*pivot, *bundleStep.Operation.Pivot) {
		return candidateProof{}, errors.New("production authoring bundle changed the signed Pivot IDs, categories, or row policies")
	}
	for _, source := range selectedProof {
		bundleSource, exists := recipeStageColumnByID(outputs[0].Construction.SourceColumns, source.ColumnID)
		if !exists || bundleSource.Name != authoringv2.ConstructionSourceProjectionName(source.ColumnID) || bundleSource.Type != source.LogicalType {
			return candidateProof{}, fmt.Errorf("compiler-owned source projection %q = %#v, want a private alias with current %s type", source.ColumnID, bundleSource, source.LogicalType)
		}
	}
	for _, want := range wantDeclared {
		bundleColumn, exists := recipeStageColumnByID(bundleStep.Outputs, want.id)
		if !exists || bundleColumn.Name != want.name || strings.Contains(bundleColumn.Name, "__construction_source_") {
			return candidateProof{}, fmt.Errorf("production bundle output %q = %#v, want public name %q", want.id, bundleColumn, want.name)
		}
	}
	capturedNames, err := capturedPublicOutputNames(captured)
	if err != nil {
		return candidateProof{}, err
	}
	wantPublicNames := []string{"status", "missing_value", "d"}
	if !reflectStringSlices(capturedNames, wantPublicNames) || !reflectStringSlices(compiled.PublicColumns, wantPublicNames) {
		return candidateProof{}, fmt.Errorf("captured/current public columns differ from literal output contract: captured=%v current=%v want=%v", capturedNames, compiled.PublicColumns, wantPublicNames)
	}
	publicSchema := make([]outputColumnProof, 0, len(wantPublicNames))
	finalSchema := make([]outputColumnProof, 0, len(compiled.OutputSchema))
	for _, column := range compiled.OutputSchema {
		proof := outputColumnProof{ID: column.ID, Name: column.Name, Label: column.Label, Kind: column.Kind,
			Nullable: column.Nullable, Internal: column.Internal, Identity: column.Identity}
		finalSchema = append(finalSchema, proof)
		if column.Internal {
			if column.ID != "__loom_row_id" || !column.Identity {
				return candidateProof{}, fmt.Errorf("unexpected internal output schema column: %#v", column)
			}
			continue
		}
		if strings.Contains(column.Name, "__construction_source_") {
			return candidateProof{}, fmt.Errorf("private source alias leaked into public physical output: %#v", column)
		}
		publicSchema = append(publicSchema, proof)
		declared := declaredByID[column.ID]
		if declared.ID == "" || declared.Name != column.Name {
			return candidateProof{}, fmt.Errorf("compiled public column %q does not retain candidate stable ID/name: %#v", column.ID, column)
		}
		wantKind := "decimal"
		if column.ID == statusID {
			wantKind = "string"
		}
		if column.Kind != wantKind || !column.Nullable || column.Label != declared.Label {
			return candidateProof{}, fmt.Errorf("compiled output metadata for %q = %#v, want nullable %s with declared label %q", column.ID, column, wantKind, declared.Label)
		}
	}
	if !reflectStringSlices(outputNames(publicSchema), wantPublicNames) || len(publicSchema) != len(wantDeclared) {
		return candidateProof{}, fmt.Errorf("final physical output schema %v differs from public contract %v", outputNames(publicSchema), wantPublicNames)
	}
	if len(finalSchema) != len(publicSchema)+1 {
		return candidateProof{}, fmt.Errorf("final output schema has %d columns including internal identity, want %d", len(finalSchema), len(publicSchema)+1)
	}
	if strings.Count(compiled.Query, "FOR root IN @@root_collection") != 1 || strings.Contains(compiled.Query, "FOR related") {
		return candidateProof{}, errors.New("current Pivot query is not a single root scan without related-row expansion")
	}
	return candidateProof{
		BaseOutputID: baseDocument.Output.ID, CandidateOutputID: proposal.OutputID, CandidateStepID: proposal.ChangedStepID,
		SelectedSources: selectedProof,
		Pivot: pivotProof{GroupKeyIDs: append([]string(nil), pivot.GroupKeyIDs...), CategoryColumnID: pivot.CategoryColumnID,
			ValueColumnID: pivot.ValueColumnID, Categories: append([]authoringv2.ConstructionPivotCategory(nil), pivot.Categories...),
			DuplicatePolicy: pivot.DuplicatePolicy, MissingCellPolicy: pivot.MissingCellPolicy,
			UnlistedCategoryPolicy: pivot.UnlistedCategoryPolicy},
		CapturedPublicNames: append([]string(nil), capturedNames...), RecompiledPublic: publicSchema,
		FinalSchema: finalSchema, SingleTerminalPivot: true, ProjectionOnlySingleRootScan: true,
	}, nil
}

func workspaceDocumentByOutputID(workspace authoringv2.Workspace, outputID string) (authoringv2.Document, bool) {
	for _, document := range workspace.Documents {
		if document.Output.ID == outputID {
			return document, true
		}
	}
	return authoringv2.Document{}, false
}

func sortedSourcePaths(selections map[string]pivotSourceSelection) []string {
	paths := make([]string, 0, len(selections))
	for path := range selections {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func isMissingCategory(value authoringv2.TableScalar) bool {
	return value.Kind == authoringv2.TableScalarMissing && value.String == nil && value.Integer == nil && value.Decimal == nil && value.Boolean == nil
}

func isStringCategory(value authoringv2.TableScalar, expected string) bool {
	return value.Kind == authoringv2.TableScalarString && value.String != nil && *value.String == expected &&
		value.Integer == nil && value.Decimal == nil && value.Boolean == nil
}

func samePivotContract(left authoringv2.ConstructionPivot, right recipe.ConstructionPivot) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

func recipeStageColumnByID(columns []recipe.StageColumn, id string) (recipe.StageColumn, bool) {
	for _, column := range columns {
		if column.ID == id {
			return column, true
		}
	}
	return recipe.StageColumn{}, false
}

func capturedPublicOutputNames(captured capturedQuery) ([]string, error) {
	returnAt := strings.LastIndex(captured.Query, "RETURN {")
	if returnAt < 0 {
		return nil, errors.New("captured query has no final public RETURN object")
	}
	matches := finalReturnProjection.FindAllStringSubmatch(captured.Query[returnAt:], -1)
	if len(matches) == 0 {
		return nil, errors.New("captured query final RETURN has no named column projections")
	}
	names := make([]string, 0, len(matches))
	seenNames := make(map[string]struct{}, len(matches))
	rowIdentityCount := 0
	for _, match := range matches {
		alias, field := match[1], match[2]
		name, ok := captured.BindVars[alias].(string)
		if !ok || name != field {
			return nil, fmt.Errorf("captured final output projection %q -> %q has bind name %v", alias, field, captured.BindVars[alias])
		}
		if name == "__loom_row_id" {
			rowIdentityCount++
			continue
		}
		if _, duplicate := seenNames[name]; duplicate {
			return nil, fmt.Errorf("captured final output repeats public column %q", name)
		}
		seenNames[name] = struct{}{}
		names = append(names, name)
	}
	if rowIdentityCount != 1 {
		return nil, fmt.Errorf("captured final RETURN has %d row identities, want one", rowIdentityCount)
	}
	return names, nil
}

func outputNames(columns []outputColumnProof) []string {
	names := make([]string, len(columns))
	for index, column := range columns {
		names[index] = column.Name
	}
	return names
}

func reflectStringSlices(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func validateCurrentIndexSpec(spec compiler.PreviewCoveringIndexSpec) error {
	wantFields := []string{"project", "dataset_generation", "auth_resource_path", "_key", "payload.id", "payload.status"}
	wantStored := []string{"payload.valueQuantity"}
	if spec.Collection != "Observation" || !strings.HasPrefix(spec.Name, "loom_pivot_preview_") ||
		!reflectStringSlices(spec.Fields, wantFields) || !reflectStringSlices(spec.StoredValues, wantStored) {
		return fmt.Errorf("compiler PreviewCoveringIndexSpec does not cover this captured Observation Pivot: %#v", spec)
	}
	return nil
}

func capabilitySnapshot(project string, catalog authoringv2.CatalogSnapshot) (capability.Snapshot, error) {
	nodes := make([]capability.Node, 0, len(catalog.Nodes))
	nodeByID := make(map[string]authoringv2.CatalogNode, len(catalog.Nodes))
	for _, node := range catalog.Nodes {
		nodeByID[node.ID] = node
		nodes = append(nodes, capability.Node{
			ID: node.ID, ResourceType: node.ResourceType, RowRootEligible: node.RowRootEligible, RowGrain: node.RowGrain,
			Populated: node.Populated, DocumentCount: intValue(node.DocumentCount), SupportedOperations: []capability.Operation{capability.OperationSelect},
		})
	}
	edges := make([]capability.Edge, 0, len(catalog.Edges))
	for _, edge := range catalog.Edges {
		n := int64(0)
		if edge.Populated {
			n = 1
		}
		edges = append(edges, capability.Edge{ID: edge.ID, FromNodeID: edge.FromNodeID, ToNodeID: edge.ToNodeID, Label: edge.Label,
			StorageDirection: edge.StorageDirection, SourceResourceType: nodeByID[edge.FromNodeID].ResourceType,
			TargetResourceType: nodeByID[edge.ToNodeID].ResourceType, ObservedEdgeCount: n})
	}
	candidates := make([]capability.Candidate, 0, len(catalog.Candidates))
	projectionModes := map[capability.ProjectionMode]bool{}
	for _, source := range catalog.Candidates {
		node, ok := nodeByID[source.NodeID]
		if !ok {
			return capability.Snapshot{}, fmt.Errorf("catalog candidate %q has no node", source.ID)
		}
		modes := make([]capability.ProjectionMode, 0, len(source.ProjectionModes))
		for _, mode := range source.ProjectionModes {
			converted, ok := projectionMode(mode)
			if !ok {
				return capability.Snapshot{}, fmt.Errorf("catalog candidate %q has unknown projection mode %q", source.ID, mode)
			}
			projectionModes[converted] = true
			modes = append(modes, converted)
		}
		operations := []capability.Operation{capability.OperationSelect}
		if source.Filterable {
			operations = append(operations, capability.OperationFilter)
		}
		if source.Chartable {
			operations = append(operations, capability.OperationChart)
		}
		boundaries := make([]capability.RepeatedBoundary, len(source.RepeatedBoundaries))
		for index, boundary := range source.RepeatedBoundaries {
			boundaries[index] = capability.RepeatedBoundary{Path: boundary.Path, MaxItems: boundary.MaxItems}
		}
		candidates = append(candidates, capability.Candidate{
			ID: source.ID, NodeID: source.NodeID, ResourceType: node.ResourceType, FieldPath: source.FieldPath, Label: source.Label,
			LogicalType: source.LogicalType, Cardinality: source.Cardinality, RepeatedBoundaries: boundaries,
			ProjectionModes: modes, SupportedOperations: operations, AggregateOperations: source.AggregateOperations,
			Observed: source.Populated, Populated: source.Populated,
		})
	}
	modes := make([]capability.ProjectionMode, 0, len(projectionModes))
	for mode := range projectionModes {
		modes = append(modes, mode)
	}
	sort.Slice(modes, func(i, j int) bool { return modes[i] < modes[j] })
	maxHops := 0
	if catalog.RoutePolicy.MaxHops != nil {
		maxHops = *catalog.RoutePolicy.MaxHops
	}
	identity := capability.SnapshotIdentity{
		Project: project, Generation: catalog.SourceGeneration, AuthorizationScopeDigest: catalog.AuthorizationScopeDigest,
		SchemaDigest:            catalog.ResolvedSchemaDigest,
		ResourceInventoryDigest: digestJSON(catalog.Nodes), RelationshipDigest: digestJSON(catalog.Edges), FieldDigest: digestJSON(catalog.Candidates),
		ProtocolVersion: "captured-public-catalog-recreated", CompilerVersion: "captured-public-catalog-recreated",
		TraversalPolicyVersion: "captured-public-catalog-recreated", ProjectionPolicyVersion: "captured-public-catalog-recreated",
	}
	if identity.Project == "" || identity.Generation == "" || identity.SchemaDigest == "" || identity.AuthorizationScopeDigest == "" {
		return capability.Snapshot{}, errors.New("captured public catalog is missing required project/generation/schema/scope identity")
	}
	policy := capability.Policy{
		Route: capability.RoutePolicy{Version: identity.TraversalPolicyVersion, MaxHops: maxHops,
			AllowsRepeatedEdges: catalog.RoutePolicy.AllowRepeatedEdges, AllowsSelfLoops: catalog.RoutePolicy.AllowSelfLoops},
		Projection: capability.ProjectionPolicy{Version: identity.ProjectionPolicyVersion, Modes: modes, SuggestionLimit: capability.DefaultSuggestionLimit},
	}
	return capability.NewSnapshot(identity, policy, capability.StatusReady, catalog.Complete, false, nodes, edges, candidates, nil), nil
}

func projectionMode(value string) (capability.ProjectionMode, bool) {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "VALUE", "SCALAR":
		return capability.ProjectionScalar, true
	case "INDEXED":
		return capability.ProjectionIndexed, true
	case "FIRST":
		return capability.ProjectionFirst, true
	case "ALL", "ARRAY":
		return capability.ProjectionArray, true
	case "DISTINCT", "DISTINCT_ARRAY":
		return capability.ProjectionDistinctArray, true
	default:
		return "", false
	}
}

func sameJSONValue(left, right any) bool {
	leftJSON, err := json.Marshal(left)
	if err != nil {
		return false
	}
	rightJSON, err := json.Marshal(right)
	return err == nil && bytes.Equal(leftJSON, rightJSON)
}

func digestJSON(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return digest(encoded)
}

func digest(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func intValue(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}
