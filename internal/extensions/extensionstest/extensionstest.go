// Package extensionstest provides reusable fake provider
// implementations for tests that need to exercise the "available"
// code paths in internal/extensions and internal/mcp without pulling
// in a real backend. Using this package from _test.go files avoids
// the drift that comes from every test file rolling its own fakes.
package extensionstest

import (
	"context"
	"sync"

	"vulpineos/internal/extensions"
)

// FakeCredentialProvider is a CredentialProvider that returns a single
// canned credential. FillCalls records every Fill invocation so tests
// can assert on the exact sequence of username/password writes. If
// FillFn is non-nil it is called in place of the default stub.
//
// All fields that might be mutated during a test are guarded by mu.
// Prefer the Set* helpers when changing fields from a goroutine that
// is not the one constructing the fake; the helpers take the write
// lock on your behalf. All read methods acquire a read lock.
type FakeCredentialProvider struct {
	mu            sync.RWMutex
	Cred          extensions.Credential
	TOTP          string
	AvailableFlag bool
	FillFn        func(ctx context.Context, credID string, target extensions.FillTarget) error

	FillCalls []FillCall
}

// FillCall captures the arguments passed to Fill.
type FillCall struct {
	CredID string
	Target extensions.FillTarget
}

// SetCred swaps the canned credential under the write lock. This is
// the recommended way to mutate the fake from a test goroutine that
// races with Lookup/List/Fill.
func (f *FakeCredentialProvider) SetCred(c extensions.Credential) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Cred = c
}

// SetAvailable toggles AvailableFlag under the write lock.
func (f *FakeCredentialProvider) SetAvailable(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.AvailableFlag = v
}

// SetTOTP overwrites the canned TOTP under the write lock.
func (f *FakeCredentialProvider) SetTOTP(v string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.TOTP = v
}

// Lookup always returns the canned credential as a copy.
func (f *FakeCredentialProvider) Lookup(ctx context.Context, siteURL string) (*extensions.Credential, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	c := f.Cred
	return &c, nil
}

// Fill records the call and delegates to FillFn if set. Always
// validates the target first, matching the contract documented on
// FillTarget.Validate.
func (f *FakeCredentialProvider) Fill(ctx context.Context, credID string, target extensions.FillTarget) error {
	if err := target.Validate(); err != nil {
		return err
	}
	f.mu.Lock()
	f.FillCalls = append(f.FillCalls, FillCall{CredID: credID, Target: target})
	fn := f.FillFn
	f.mu.Unlock()
	if fn != nil {
		return fn(ctx, credID, target)
	}
	return nil
}

// GenerateCode returns TOTP if non-empty, otherwise "000000".
func (f *FakeCredentialProvider) GenerateCode(ctx context.Context, credID string) (string, error) {
	f.mu.RLock()
	totp := f.TOTP
	f.mu.RUnlock()
	if totp != "" {
		return totp, nil
	}
	return "000000", nil
}

// List returns a single-element slice containing the canned credential.
func (f *FakeCredentialProvider) List(ctx context.Context) ([]extensions.Credential, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return []extensions.Credential{f.Cred}, nil
}

// Available reports AvailableFlag.
func (f *FakeCredentialProvider) Available() bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.AvailableFlag
}

// RecordedFills returns a snapshot copy of FillCalls, safe for assertion
// from the test goroutine.
func (f *FakeCredentialProvider) RecordedFills() []FillCall {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]FillCall, len(f.FillCalls))
	copy(out, f.FillCalls)
	return out
}

// FakeAudioCapturer is an AudioCapturer that records Start requests
// and returns canned handles. StartReq is updated on every Start call
// so tests can assert that defaults were applied.
type FakeAudioCapturer struct {
	mu            sync.RWMutex
	AvailableFlag bool
	Handle        extensions.CaptureHandle
	Chunk         []byte
	EOF           bool

	StartReq extensions.CaptureRequest
	StopID   string
	ReadID   string
}

// SetAvailable toggles AvailableFlag under the write lock.
func (f *FakeAudioCapturer) SetAvailable(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.AvailableFlag = v
}

// SetHandle swaps the canned handle under the write lock.
func (f *FakeAudioCapturer) SetHandle(h extensions.CaptureHandle) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Handle = h
}

// SetChunk swaps the canned chunk/EOF pair under the write lock.
func (f *FakeAudioCapturer) SetChunk(chunk []byte, eof bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Chunk = chunk
	f.EOF = eof
}

// Start records the request and returns the canned handle.
func (f *FakeAudioCapturer) Start(ctx context.Context, req extensions.CaptureRequest) (*extensions.CaptureHandle, error) {
	f.mu.Lock()
	f.StartReq = req
	h := f.Handle
	f.mu.Unlock()
	return &h, nil
}

// Stop records the stop handle ID.
func (f *FakeAudioCapturer) Stop(ctx context.Context, handleID string) error {
	f.mu.Lock()
	f.StopID = handleID
	f.mu.Unlock()
	return nil
}

// Read records the read handle ID and returns the canned chunk.
func (f *FakeAudioCapturer) Read(ctx context.Context, handleID string, maxBytes int) ([]byte, bool, error) {
	f.mu.Lock()
	f.ReadID = handleID
	chunk := f.Chunk
	eof := f.EOF
	f.mu.Unlock()
	return chunk, eof, nil
}

// Available reports AvailableFlag.
func (f *FakeAudioCapturer) Available() bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.AvailableFlag
}

// LastStartRequest returns the most recent CaptureRequest seen by Start.
func (f *FakeAudioCapturer) LastStartRequest() extensions.CaptureRequest {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.StartReq
}

// FakeMobileBridge is a MobileBridge that returns canned devices. All
// fields are guarded by mu so tests can mutate the fake from
// concurrent goroutines without data races under -race.
type FakeMobileBridge struct {
	mu            sync.RWMutex
	AvailableFlag bool
	Devices       []extensions.MobileDevice
	Session       extensions.MobileSession
	Disconnected  []string
}

// SetAvailable toggles AvailableFlag under the write lock.
func (f *FakeMobileBridge) SetAvailable(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.AvailableFlag = v
}

// SetDevices replaces the canned device slice under the write lock.
func (f *FakeMobileBridge) SetDevices(d []extensions.MobileDevice) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Devices = d
}

// SetSession replaces the canned session under the write lock.
func (f *FakeMobileBridge) SetSession(s extensions.MobileSession) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Session = s
}

// ListDevices returns a copy of the canned device slice.
func (f *FakeMobileBridge) ListDevices(ctx context.Context) ([]extensions.MobileDevice, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]extensions.MobileDevice, len(f.Devices))
	copy(out, f.Devices)
	return out, nil
}

// Connect returns the canned session.
func (f *FakeMobileBridge) Connect(ctx context.Context, udid string) (*extensions.MobileSession, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	s := f.Session
	if s.UDID == "" {
		s.UDID = udid
	}
	return &s, nil
}

// Disconnect records the closed session ID.
func (f *FakeMobileBridge) Disconnect(ctx context.Context, sessionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Disconnected = append(f.Disconnected, sessionID)
	return nil
}

// Available reports AvailableFlag.
func (f *FakeMobileBridge) Available() bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.AvailableFlag
}

// FakeCaptchaProvider records captcha/challenge requests and returns
// canned challenge and solution metadata for MCP and extension tests.
type FakeCaptchaProvider struct {
	mu            sync.RWMutex
	AvailableFlag bool
	Challenge     extensions.CaptchaChallenge
	Solution      extensions.CaptchaSolution
	ApplyErr      error

	DetectReq extensions.CaptchaDetectRequest
	SolveReq  extensions.CaptchaSolveRequest
	ApplyReq  extensions.CaptchaApplyRequest
}

func (f *FakeCaptchaProvider) SetAvailable(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.AvailableFlag = v
}

func (f *FakeCaptchaProvider) SetChallenge(c extensions.CaptchaChallenge) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Challenge = c
}

func (f *FakeCaptchaProvider) SetSolution(s extensions.CaptchaSolution) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Solution = s
}

func (f *FakeCaptchaProvider) Detect(ctx context.Context, req extensions.CaptchaDetectRequest) (*extensions.CaptchaChallenge, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.DetectReq = req
	challenge := f.Challenge
	return &challenge, nil
}

func (f *FakeCaptchaProvider) Solve(ctx context.Context, req extensions.CaptchaSolveRequest) (*extensions.CaptchaSolution, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.SolveReq = req
	solution := f.Solution
	return &solution, nil
}

func (f *FakeCaptchaProvider) Apply(ctx context.Context, req extensions.CaptchaApplyRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ApplyReq = req
	return f.ApplyErr
}

func (f *FakeCaptchaProvider) Available() bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.AvailableFlag
}

func (f *FakeCaptchaProvider) LastDetectRequest() extensions.CaptchaDetectRequest {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.DetectReq
}

func (f *FakeCaptchaProvider) LastSolveRequest() extensions.CaptchaSolveRequest {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.SolveReq
}

func (f *FakeCaptchaProvider) LastApplyRequest() extensions.CaptchaApplyRequest {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.ApplyReq
}

// FakeSentinelProvider records events and outcomes while returning
// canned status and variant bundles.
type FakeSentinelProvider struct {
	mu                        sync.RWMutex
	AvailableFlag             bool
	StatusValue               extensions.SentinelStatus
	VariantBundles            []extensions.SentinelVariantBundle
	TrustRecipes              []extensions.SentinelTrustRecipe
	MaturityMetrics           []extensions.SentinelMaturityMetric
	AssignmentRules           []extensions.SentinelAssignmentRule
	SessionTimelines          []extensions.SentinelSessionTimeline
	OutcomeLabels             []extensions.SentinelOutcomeLabel
	OutcomeSummary            []extensions.SentinelOutcomeSummary
	ProbeSummary              []extensions.SentinelProbeSummary
	TrustActivity             []extensions.SentinelTrustActivitySummary
	TrustEffectiveness        []extensions.SentinelTrustEffectivenessSummary
	TrustAssets               []extensions.SentinelTrustAssetSummary
	MaturityEvidence          []extensions.SentinelMaturityEvidenceSummary
	TransportEvidence         []extensions.SentinelTransportEvidenceSummary
	CoherenceDiff             []extensions.SentinelCoherenceDiffSummary
	StageSummary              []extensions.SentinelStageSummary
	AssignmentRecommendations []extensions.SentinelAssignmentRecommendation
	TrustStrategy             []extensions.SentinelTrustStrategySummary
	TrustRecipeStrategy       []extensions.SentinelTrustRecipeStrategySummary
	TrustRollout              []extensions.SentinelTrustRolloutSummary
	TrustRolloutDebt          []extensions.SentinelTrustRolloutDebtSummary
	CanarySummary             []extensions.SentinelCanarySummary
	VariantCompareSummary     []extensions.SentinelVariantCompareSummary
	SiteIntelligenceSummary   []extensions.SentinelSiteIntelligenceSummary
	ProbeSequenceSummary      []extensions.SentinelProbeSequenceSummary
	VendorIntelligenceSummary []extensions.SentinelVendorIntelligenceSummary
	VendorEffectiveness       []extensions.SentinelVendorEffectivenessSummary
	VendorUplift              []extensions.SentinelVendorUpliftSummary
	VendorRollout             []extensions.SentinelVendorRolloutSummary
	TrustPlaybook             []extensions.SentinelTrustPlaybookSummary
	ExperimentGaps            []extensions.SentinelExperimentGapSummary
	SitePressure              []extensions.SentinelSitePressureSummary
	PatchQueue                []extensions.SentinelPatchCandidate
	TrustRepairQueue          []extensions.SentinelTrustRepairQueueSummary
	PatchInvestment           []extensions.SentinelPatchInvestmentSummary
	SurfaceHotspots           []extensions.SentinelSurfaceHotspotSummary
	SurfaceEffectiveness      []extensions.SentinelSurfaceEffectivenessSummary
	SurfaceStrategy           []extensions.SentinelSurfaceStrategySummary
	ExperimentBoard           []extensions.SentinelExperimentSummary
	LastTimelineFilter        extensions.SentinelTimelineFilter
	Events                    []extensions.SentinelEvent
	Outcomes                  []extensions.SentinelOutcome
}

// SetAvailable toggles AvailableFlag under the write lock.
func (f *FakeSentinelProvider) SetAvailable(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.AvailableFlag = v
}

// SetStatus replaces the canned status under the write lock.
func (f *FakeSentinelProvider) SetStatus(s extensions.SentinelStatus) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.StatusValue = s
}

// SetVariantBundles replaces the canned variant bundles under the write lock.
func (f *FakeSentinelProvider) SetVariantBundles(v []extensions.SentinelVariantBundle) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.VariantBundles = append([]extensions.SentinelVariantBundle(nil), v...)
}

// SetTrustRecipes replaces the canned trust recipes under the write lock.
func (f *FakeSentinelProvider) SetTrustRecipes(v []extensions.SentinelTrustRecipe) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.TrustRecipes = append([]extensions.SentinelTrustRecipe(nil), v...)
}

// SetMaturityMetrics replaces the canned maturity metrics under the write lock.
func (f *FakeSentinelProvider) SetMaturityMetrics(v []extensions.SentinelMaturityMetric) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.MaturityMetrics = append([]extensions.SentinelMaturityMetric(nil), v...)
}

// SetAssignmentRules replaces the canned assignment rules under the write lock.
func (f *FakeSentinelProvider) SetAssignmentRules(v []extensions.SentinelAssignmentRule) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.AssignmentRules = append([]extensions.SentinelAssignmentRule(nil), v...)
}

// SetSessionTimelines replaces the canned session timelines under the write lock.
func (f *FakeSentinelProvider) SetSessionTimelines(v []extensions.SentinelSessionTimeline) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.SessionTimelines = append([]extensions.SentinelSessionTimeline(nil), v...)
}

// SetOutcomeLabels replaces the canned outcome labels under the write lock.
func (f *FakeSentinelProvider) SetOutcomeLabels(v []extensions.SentinelOutcomeLabel) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.OutcomeLabels = append([]extensions.SentinelOutcomeLabel(nil), v...)
}

// SetOutcomeSummary replaces the canned outcome summary under the write lock.
func (f *FakeSentinelProvider) SetOutcomeSummary(v []extensions.SentinelOutcomeSummary) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.OutcomeSummary = append([]extensions.SentinelOutcomeSummary(nil), v...)
}

// SetProbeSummary replaces the canned probe summary rows under the write lock.
func (f *FakeSentinelProvider) SetProbeSummary(v []extensions.SentinelProbeSummary) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ProbeSummary = append([]extensions.SentinelProbeSummary(nil), v...)
}

// SetTrustActivity replaces the canned trust activity rows under the write lock.
func (f *FakeSentinelProvider) SetTrustActivity(v []extensions.SentinelTrustActivitySummary) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.TrustActivity = append([]extensions.SentinelTrustActivitySummary(nil), v...)
}

// SetTrustEffectiveness replaces the canned trust-effectiveness rows under the write lock.
func (f *FakeSentinelProvider) SetTrustEffectiveness(v []extensions.SentinelTrustEffectivenessSummary) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.TrustEffectiveness = append([]extensions.SentinelTrustEffectivenessSummary(nil), v...)
}

// SetMaturityEvidence replaces the canned maturity-evidence rows under the write lock.
func (f *FakeSentinelProvider) SetMaturityEvidence(v []extensions.SentinelMaturityEvidenceSummary) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.MaturityEvidence = append([]extensions.SentinelMaturityEvidenceSummary(nil), v...)
}

// SetTransportEvidence replaces the canned transport-evidence rows under the write lock.
func (f *FakeSentinelProvider) SetTransportEvidence(v []extensions.SentinelTransportEvidenceSummary) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.TransportEvidence = append([]extensions.SentinelTransportEvidenceSummary(nil), v...)
}

// SetCoherenceDiff replaces the canned coherence-diff rows under the write lock.
func (f *FakeSentinelProvider) SetCoherenceDiff(v []extensions.SentinelCoherenceDiffSummary) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.CoherenceDiff = append([]extensions.SentinelCoherenceDiffSummary(nil), v...)
}

// SetStageSummary replaces the canned stage-summary rows under the write lock.
func (f *FakeSentinelProvider) SetStageSummary(v []extensions.SentinelStageSummary) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.StageSummary = append([]extensions.SentinelStageSummary(nil), v...)
}

// SetAssignmentRecommendations replaces the canned assignment recommendations.
func (f *FakeSentinelProvider) SetAssignmentRecommendations(v []extensions.SentinelAssignmentRecommendation) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.AssignmentRecommendations = append([]extensions.SentinelAssignmentRecommendation(nil), v...)
}

// SetTrustStrategy replaces the canned trust-strategy rows.
func (f *FakeSentinelProvider) SetTrustStrategy(v []extensions.SentinelTrustStrategySummary) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.TrustStrategy = append([]extensions.SentinelTrustStrategySummary(nil), v...)
}

// SetTrustRecipeStrategy replaces the canned recipe-strategy rows.
func (f *FakeSentinelProvider) SetTrustRecipeStrategy(v []extensions.SentinelTrustRecipeStrategySummary) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.TrustRecipeStrategy = append([]extensions.SentinelTrustRecipeStrategySummary(nil), v...)
}

// SetTrustRollout replaces the canned trust-rollout rows.
func (f *FakeSentinelProvider) SetTrustRollout(v []extensions.SentinelTrustRolloutSummary) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.TrustRollout = append([]extensions.SentinelTrustRolloutSummary(nil), v...)
}

// SetTrustRolloutDebt replaces the canned trust-rollout debt rows.
func (f *FakeSentinelProvider) SetTrustRolloutDebt(v []extensions.SentinelTrustRolloutDebtSummary) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.TrustRolloutDebt = append([]extensions.SentinelTrustRolloutDebtSummary(nil), v...)
}

// SetCanarySummary replaces the canned canary rows.
func (f *FakeSentinelProvider) SetCanarySummary(v []extensions.SentinelCanarySummary) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.CanarySummary = append([]extensions.SentinelCanarySummary(nil), v...)
}

// SetVariantCompareSummary replaces the canned variant-compare rows.
func (f *FakeSentinelProvider) SetVariantCompareSummary(v []extensions.SentinelVariantCompareSummary) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.VariantCompareSummary = append([]extensions.SentinelVariantCompareSummary(nil), v...)
}

// SetSiteIntelligenceSummary replaces the canned site-intelligence rows.
func (f *FakeSentinelProvider) SetSiteIntelligenceSummary(v []extensions.SentinelSiteIntelligenceSummary) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.SiteIntelligenceSummary = append([]extensions.SentinelSiteIntelligenceSummary(nil), v...)
}

// SetProbeSequenceSummary replaces the canned probe-sequence rows.
func (f *FakeSentinelProvider) SetProbeSequenceSummary(v []extensions.SentinelProbeSequenceSummary) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ProbeSequenceSummary = append([]extensions.SentinelProbeSequenceSummary(nil), v...)
}

// SetVendorIntelligenceSummary replaces the canned vendor-intelligence rows.
func (f *FakeSentinelProvider) SetVendorIntelligenceSummary(v []extensions.SentinelVendorIntelligenceSummary) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.VendorIntelligenceSummary = append([]extensions.SentinelVendorIntelligenceSummary(nil), v...)
}

// SetVendorEffectiveness replaces the canned vendor-effectiveness rows.
func (f *FakeSentinelProvider) SetVendorEffectiveness(v []extensions.SentinelVendorEffectivenessSummary) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.VendorEffectiveness = append([]extensions.SentinelVendorEffectivenessSummary(nil), v...)
}

// SetVendorUplift replaces the canned vendor-uplift rows.
func (f *FakeSentinelProvider) SetVendorUplift(v []extensions.SentinelVendorUpliftSummary) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.VendorUplift = append([]extensions.SentinelVendorUpliftSummary(nil), v...)
}

// SetVendorRollout replaces the canned vendor-rollout rows.
func (f *FakeSentinelProvider) SetVendorRollout(v []extensions.SentinelVendorRolloutSummary) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.VendorRollout = append([]extensions.SentinelVendorRolloutSummary(nil), v...)
}

// SetTrustPlaybook replaces the canned trust-playbook rows.
func (f *FakeSentinelProvider) SetTrustPlaybook(v []extensions.SentinelTrustPlaybookSummary) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.TrustPlaybook = append([]extensions.SentinelTrustPlaybookSummary(nil), v...)
}

// SetExperimentGaps replaces the canned experiment-gap rows.
func (f *FakeSentinelProvider) SetExperimentGaps(v []extensions.SentinelExperimentGapSummary) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ExperimentGaps = append([]extensions.SentinelExperimentGapSummary(nil), v...)
}

// SetSitePressure replaces the canned site pressure rows under the write lock.
func (f *FakeSentinelProvider) SetSitePressure(v []extensions.SentinelSitePressureSummary) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.SitePressure = append([]extensions.SentinelSitePressureSummary(nil), v...)
}

// SetPatchQueue replaces the canned patch queue rows under the write lock.
func (f *FakeSentinelProvider) SetPatchQueue(v []extensions.SentinelPatchCandidate) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.PatchQueue = append([]extensions.SentinelPatchCandidate(nil), v...)
}

// SetTrustRepairQueue replaces the canned trust repair queue rows.
func (f *FakeSentinelProvider) SetTrustRepairQueue(v []extensions.SentinelTrustRepairQueueSummary) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.TrustRepairQueue = append([]extensions.SentinelTrustRepairQueueSummary(nil), v...)
}

// SetPatchInvestment replaces the canned patch investment rows under the write lock.
func (f *FakeSentinelProvider) SetPatchInvestment(v []extensions.SentinelPatchInvestmentSummary) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.PatchInvestment = append([]extensions.SentinelPatchInvestmentSummary(nil), v...)
}

// SetSurfaceHotspots replaces the canned surface hotspot rows under the write lock.
func (f *FakeSentinelProvider) SetSurfaceHotspots(v []extensions.SentinelSurfaceHotspotSummary) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.SurfaceHotspots = append([]extensions.SentinelSurfaceHotspotSummary(nil), v...)
}

// SetSurfaceEffectiveness replaces the canned surface-effectiveness rows.
func (f *FakeSentinelProvider) SetSurfaceEffectiveness(v []extensions.SentinelSurfaceEffectivenessSummary) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.SurfaceEffectiveness = append([]extensions.SentinelSurfaceEffectivenessSummary(nil), v...)
}

// SetSurfaceStrategy replaces the canned surface-strategy rows.
func (f *FakeSentinelProvider) SetSurfaceStrategy(v []extensions.SentinelSurfaceStrategySummary) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.SurfaceStrategy = append([]extensions.SentinelSurfaceStrategySummary(nil), v...)
}

// SetExperimentBoard replaces the canned experiment summary rows.
func (f *FakeSentinelProvider) SetExperimentBoard(v []extensions.SentinelExperimentSummary) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ExperimentBoard = append([]extensions.SentinelExperimentSummary(nil), v...)
}

// Status returns the canned status.
func (f *FakeSentinelProvider) Status(ctx context.Context) (*extensions.SentinelStatus, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	status := f.StatusValue
	return &status, nil
}

// RecordEvent appends the event under the write lock.
func (f *FakeSentinelProvider) RecordEvent(ctx context.Context, event extensions.SentinelEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Events = append(f.Events, event)
	return nil
}

// RecordOutcome appends the outcome under the write lock.
func (f *FakeSentinelProvider) RecordOutcome(ctx context.Context, outcome extensions.SentinelOutcome) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Outcomes = append(f.Outcomes, outcome)
	return nil
}

// ListVariantBundles returns a copy of the canned variant bundles.
func (f *FakeSentinelProvider) ListVariantBundles(ctx context.Context) ([]extensions.SentinelVariantBundle, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]extensions.SentinelVariantBundle, len(f.VariantBundles))
	copy(out, f.VariantBundles)
	return out, nil
}

// ListTrustRecipes returns a copy of the canned trust recipes.
func (f *FakeSentinelProvider) ListTrustRecipes(ctx context.Context) ([]extensions.SentinelTrustRecipe, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]extensions.SentinelTrustRecipe, len(f.TrustRecipes))
	copy(out, f.TrustRecipes)
	return out, nil
}

// ListMaturityMetrics returns a copy of the canned maturity metrics.
func (f *FakeSentinelProvider) ListMaturityMetrics(ctx context.Context) ([]extensions.SentinelMaturityMetric, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]extensions.SentinelMaturityMetric, len(f.MaturityMetrics))
	copy(out, f.MaturityMetrics)
	return out, nil
}

// ListAssignmentRules returns a copy of the canned assignment rules.
func (f *FakeSentinelProvider) ListAssignmentRules(ctx context.Context) ([]extensions.SentinelAssignmentRule, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]extensions.SentinelAssignmentRule, len(f.AssignmentRules))
	copy(out, f.AssignmentRules)
	return out, nil
}

// ListSessionTimelines returns a copy of the canned session timelines.
func (f *FakeSentinelProvider) ListSessionTimelines(ctx context.Context, filter extensions.SentinelTimelineFilter) ([]extensions.SentinelSessionTimeline, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.LastTimelineFilter = filter
	out := make([]extensions.SentinelSessionTimeline, len(f.SessionTimelines))
	copy(out, f.SessionTimelines)
	return out, nil
}

// ListOutcomeLabels returns a copy of the canned outcome labels.
func (f *FakeSentinelProvider) ListOutcomeLabels(ctx context.Context) ([]extensions.SentinelOutcomeLabel, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]extensions.SentinelOutcomeLabel, len(f.OutcomeLabels))
	copy(out, f.OutcomeLabels)
	return out, nil
}

// SummarizeOutcomes returns a copy of the canned outcome summary rows.
func (f *FakeSentinelProvider) SummarizeOutcomes(ctx context.Context) ([]extensions.SentinelOutcomeSummary, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]extensions.SentinelOutcomeSummary, len(f.OutcomeSummary))
	copy(out, f.OutcomeSummary)
	return out, nil
}

// SummarizeProbes returns a copy of the canned probe summary rows.
func (f *FakeSentinelProvider) SummarizeProbes(ctx context.Context) ([]extensions.SentinelProbeSummary, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]extensions.SentinelProbeSummary, len(f.ProbeSummary))
	copy(out, f.ProbeSummary)
	return out, nil
}

// SummarizeTrustActivity returns a copy of the canned trust activity rows.
func (f *FakeSentinelProvider) SummarizeTrustActivity(ctx context.Context) ([]extensions.SentinelTrustActivitySummary, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]extensions.SentinelTrustActivitySummary, len(f.TrustActivity))
	copy(out, f.TrustActivity)
	return out, nil
}

// SummarizeTrustEffectiveness returns a copy of the canned trust-effectiveness rows.
func (f *FakeSentinelProvider) SummarizeTrustEffectiveness(ctx context.Context) ([]extensions.SentinelTrustEffectivenessSummary, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]extensions.SentinelTrustEffectivenessSummary, len(f.TrustEffectiveness))
	copy(out, f.TrustEffectiveness)
	return out, nil
}

// SummarizeTrustAssets returns a copy of the canned trust-asset rows.
func (f *FakeSentinelProvider) SummarizeTrustAssets(ctx context.Context) ([]extensions.SentinelTrustAssetSummary, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]extensions.SentinelTrustAssetSummary, len(f.TrustAssets))
	copy(out, f.TrustAssets)
	return out, nil
}

// SummarizeMaturityEvidence returns a copy of the canned maturity-evidence rows.
func (f *FakeSentinelProvider) SummarizeMaturityEvidence(ctx context.Context) ([]extensions.SentinelMaturityEvidenceSummary, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]extensions.SentinelMaturityEvidenceSummary, len(f.MaturityEvidence))
	copy(out, f.MaturityEvidence)
	return out, nil
}

// SummarizeTransportEvidence returns a copy of the canned transport-evidence rows.
func (f *FakeSentinelProvider) SummarizeTransportEvidence(ctx context.Context) ([]extensions.SentinelTransportEvidenceSummary, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]extensions.SentinelTransportEvidenceSummary, len(f.TransportEvidence))
	copy(out, f.TransportEvidence)
	return out, nil
}

// SummarizeCoherenceDiff returns a copy of the canned coherence-diff rows.
func (f *FakeSentinelProvider) SummarizeCoherenceDiff(ctx context.Context) ([]extensions.SentinelCoherenceDiffSummary, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]extensions.SentinelCoherenceDiffSummary, len(f.CoherenceDiff))
	copy(out, f.CoherenceDiff)
	return out, nil
}

// SummarizeStages returns a copy of the canned stage-summary rows.
func (f *FakeSentinelProvider) SummarizeStages(ctx context.Context) ([]extensions.SentinelStageSummary, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]extensions.SentinelStageSummary, len(f.StageSummary))
	copy(out, f.StageSummary)
	return out, nil
}

// SummarizeAssignmentRecommendations returns a copy of the canned recommendations.
func (f *FakeSentinelProvider) SummarizeAssignmentRecommendations(ctx context.Context) ([]extensions.SentinelAssignmentRecommendation, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]extensions.SentinelAssignmentRecommendation, len(f.AssignmentRecommendations))
	copy(out, f.AssignmentRecommendations)
	return out, nil
}

// SummarizeTrustStrategy returns a copy of the canned trust-strategy rows.
func (f *FakeSentinelProvider) SummarizeTrustStrategy(ctx context.Context) ([]extensions.SentinelTrustStrategySummary, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]extensions.SentinelTrustStrategySummary, len(f.TrustStrategy))
	copy(out, f.TrustStrategy)
	return out, nil
}

// SummarizeTrustRecipeStrategy returns a copy of the canned recipe-strategy rows.
func (f *FakeSentinelProvider) SummarizeTrustRecipeStrategy(ctx context.Context) ([]extensions.SentinelTrustRecipeStrategySummary, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]extensions.SentinelTrustRecipeStrategySummary, len(f.TrustRecipeStrategy))
	copy(out, f.TrustRecipeStrategy)
	return out, nil
}

// SummarizeTrustRollout returns a copy of the canned trust-rollout rows.
func (f *FakeSentinelProvider) SummarizeTrustRollout(ctx context.Context) ([]extensions.SentinelTrustRolloutSummary, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]extensions.SentinelTrustRolloutSummary, len(f.TrustRollout))
	copy(out, f.TrustRollout)
	return out, nil
}

// SummarizeTrustRolloutDebt returns a copy of the canned trust-rollout debt rows.
func (f *FakeSentinelProvider) SummarizeTrustRolloutDebt(ctx context.Context) ([]extensions.SentinelTrustRolloutDebtSummary, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]extensions.SentinelTrustRolloutDebtSummary, len(f.TrustRolloutDebt))
	copy(out, f.TrustRolloutDebt)
	return out, nil
}

// SummarizeCanaries returns a copy of the canned canary rows.
func (f *FakeSentinelProvider) SummarizeCanaries(ctx context.Context) ([]extensions.SentinelCanarySummary, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]extensions.SentinelCanarySummary, len(f.CanarySummary))
	copy(out, f.CanarySummary)
	return out, nil
}

// SummarizeVariantCompare returns a copy of the canned compare rows.
func (f *FakeSentinelProvider) SummarizeVariantCompare(ctx context.Context) ([]extensions.SentinelVariantCompareSummary, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]extensions.SentinelVariantCompareSummary, len(f.VariantCompareSummary))
	copy(out, f.VariantCompareSummary)
	return out, nil
}

// SummarizeSiteIntelligence returns a copy of the canned site-intelligence rows.
func (f *FakeSentinelProvider) SummarizeSiteIntelligence(ctx context.Context) ([]extensions.SentinelSiteIntelligenceSummary, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]extensions.SentinelSiteIntelligenceSummary, len(f.SiteIntelligenceSummary))
	copy(out, f.SiteIntelligenceSummary)
	return out, nil
}

// SummarizeProbeSequences returns a copy of the canned probe-sequence rows.
func (f *FakeSentinelProvider) SummarizeProbeSequences(ctx context.Context) ([]extensions.SentinelProbeSequenceSummary, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]extensions.SentinelProbeSequenceSummary, len(f.ProbeSequenceSummary))
	copy(out, f.ProbeSequenceSummary)
	return out, nil
}

// SummarizeVendorIntelligence returns a copy of the canned vendor-intelligence rows.
func (f *FakeSentinelProvider) SummarizeVendorIntelligence(ctx context.Context) ([]extensions.SentinelVendorIntelligenceSummary, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]extensions.SentinelVendorIntelligenceSummary, len(f.VendorIntelligenceSummary))
	copy(out, f.VendorIntelligenceSummary)
	return out, nil
}

// SummarizeVendorEffectiveness returns a copy of the canned vendor-effectiveness rows.
func (f *FakeSentinelProvider) SummarizeVendorEffectiveness(ctx context.Context) ([]extensions.SentinelVendorEffectivenessSummary, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]extensions.SentinelVendorEffectivenessSummary, len(f.VendorEffectiveness))
	copy(out, f.VendorEffectiveness)
	return out, nil
}

// SummarizeVendorUplift returns a copy of the canned vendor-uplift rows.
func (f *FakeSentinelProvider) SummarizeVendorUplift(ctx context.Context) ([]extensions.SentinelVendorUpliftSummary, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]extensions.SentinelVendorUpliftSummary, len(f.VendorUplift))
	copy(out, f.VendorUplift)
	return out, nil
}

// SummarizeVendorRollout returns a copy of the canned vendor-rollout rows.
func (f *FakeSentinelProvider) SummarizeVendorRollout(ctx context.Context) ([]extensions.SentinelVendorRolloutSummary, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]extensions.SentinelVendorRolloutSummary, len(f.VendorRollout))
	copy(out, f.VendorRollout)
	return out, nil
}

// SummarizeTrustPlaybook returns a copy of the canned trust-playbook rows.
func (f *FakeSentinelProvider) SummarizeTrustPlaybook(ctx context.Context) ([]extensions.SentinelTrustPlaybookSummary, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]extensions.SentinelTrustPlaybookSummary, len(f.TrustPlaybook))
	copy(out, f.TrustPlaybook)
	return out, nil
}

// SummarizeExperimentGaps returns a copy of the canned experiment-gap rows.
func (f *FakeSentinelProvider) SummarizeExperimentGaps(ctx context.Context) ([]extensions.SentinelExperimentGapSummary, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]extensions.SentinelExperimentGapSummary, len(f.ExperimentGaps))
	copy(out, f.ExperimentGaps)
	return out, nil
}

// SummarizeSitePressure returns a copy of the canned site pressure rows.
func (f *FakeSentinelProvider) SummarizeSitePressure(ctx context.Context) ([]extensions.SentinelSitePressureSummary, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]extensions.SentinelSitePressureSummary, len(f.SitePressure))
	copy(out, f.SitePressure)
	return out, nil
}

// SummarizePatchQueue returns a copy of the canned patch queue rows.
func (f *FakeSentinelProvider) SummarizePatchQueue(ctx context.Context) ([]extensions.SentinelPatchCandidate, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]extensions.SentinelPatchCandidate, len(f.PatchQueue))
	copy(out, f.PatchQueue)
	return out, nil
}

// SummarizeTrustRepairQueue returns a copy of the canned trust repair queue rows.
func (f *FakeSentinelProvider) SummarizeTrustRepairQueue(ctx context.Context) ([]extensions.SentinelTrustRepairQueueSummary, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]extensions.SentinelTrustRepairQueueSummary, len(f.TrustRepairQueue))
	copy(out, f.TrustRepairQueue)
	return out, nil
}

// SummarizePatchInvestment returns a copy of the canned patch-investment rows.
func (f *FakeSentinelProvider) SummarizePatchInvestment(ctx context.Context) ([]extensions.SentinelPatchInvestmentSummary, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]extensions.SentinelPatchInvestmentSummary, len(f.PatchInvestment))
	copy(out, f.PatchInvestment)
	return out, nil
}

// SummarizeSurfaceHotspots returns a copy of the canned surface hotspot rows.
func (f *FakeSentinelProvider) SummarizeSurfaceHotspots(ctx context.Context) ([]extensions.SentinelSurfaceHotspotSummary, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]extensions.SentinelSurfaceHotspotSummary, len(f.SurfaceHotspots))
	copy(out, f.SurfaceHotspots)
	return out, nil
}

// SummarizeSurfaceEffectiveness returns a copy of the canned surface-effectiveness rows.
func (f *FakeSentinelProvider) SummarizeSurfaceEffectiveness(ctx context.Context) ([]extensions.SentinelSurfaceEffectivenessSummary, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]extensions.SentinelSurfaceEffectivenessSummary, len(f.SurfaceEffectiveness))
	copy(out, f.SurfaceEffectiveness)
	return out, nil
}

// SummarizeSurfaceStrategy returns a copy of the canned surface-strategy rows.
func (f *FakeSentinelProvider) SummarizeSurfaceStrategy(ctx context.Context) ([]extensions.SentinelSurfaceStrategySummary, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]extensions.SentinelSurfaceStrategySummary, len(f.SurfaceStrategy))
	copy(out, f.SurfaceStrategy)
	return out, nil
}

// SummarizeExperiments returns a copy of the canned experiment board rows.
func (f *FakeSentinelProvider) SummarizeExperiments(ctx context.Context) ([]extensions.SentinelExperimentSummary, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]extensions.SentinelExperimentSummary, len(f.ExperimentBoard))
	copy(out, f.ExperimentBoard)
	return out, nil
}

// Available reports AvailableFlag.
func (f *FakeSentinelProvider) Available() bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.AvailableFlag
}

// RecordedEvents returns a snapshot copy of the captured events.
func (f *FakeSentinelProvider) RecordedEvents() []extensions.SentinelEvent {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]extensions.SentinelEvent, len(f.Events))
	copy(out, f.Events)
	return out
}

// RecordedOutcomes returns a snapshot copy of the captured outcomes.
func (f *FakeSentinelProvider) RecordedOutcomes() []extensions.SentinelOutcome {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]extensions.SentinelOutcome, len(f.Outcomes))
	copy(out, f.Outcomes)
	return out
}
