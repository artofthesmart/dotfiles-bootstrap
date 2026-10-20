package app

import "testing"

func TestExitForIssue(t *testing.T) {
	tests := []struct {
		code IssueCode
		want ExitCode
	}{
		{IssueHostUnsupported, ExitUnsupportedHost},
		{IssueArchitectureUnsupported, ExitUnsupportedHost},
		{IssueDesktopRequired, ExitUnsupportedHost},
		{IssueHostVersionUnsupported, ExitUnsupportedHost},
		{IssuePrerequisiteMissing, ExitPrerequisiteFailure},
		{IssuePrerequisiteVersion, ExitPrerequisiteFailure},
		{IssuePrerequisiteUnusable, ExitPrerequisiteFailure},
		{IssuePolicyBlocked, ExitPolicyOrPermissionFailure},
		{IssueConsentDeclined, ExitCancelled},
		{IssueManualApproval, ExitManualRequired},
		{IssueNetworkUnavailable, ExitNetworkFailure},
		{IssueHTTPSPolicy, ExitIntegrityFailure},
		{IssueDownloadInvalid, ExitNetworkFailure},
		{IssueChecksumMismatch, ExitIntegrityFailure},
		{IssuePublisherInvalid, ExitIntegrityFailure},
		{IssueMetadataInconsistent, ExitIntegrityFailure},
		{IssueCapacityInsufficient, ExitInsufficientCapacity},
		{IssueMemoryInsufficient, ExitInsufficientCapacity},
		{IssueCapacityUnknown, ExitInsufficientCapacity},
		{IssueModelIncompatible, ExitModelIncompatible},
		{IssueModelContextInsufficient, ExitModelIncompatible},
		{IssueModelRemote, ExitModelIncompatible},
		{IssueModelToolsUnsupported, ExitModelIncompatible},
		{IssueModelAcquisition, ExitModelAcquisitionFailure},
		{IssueModelAlias, ExitModelAcquisitionFailure},
		{IssueRuntimeUnavailable, ExitRuntimeVerificationFailure},
		{IssueRuntimeToolCall, ExitRuntimeVerificationFailure},
		{IssueRuntimeContext, ExitRuntimeVerificationFailure},
		{IssueRuntimeOffloadUnknown, ExitRuntimeVerificationFailure},
		{IssueRuntimeRestartRequired, ExitManualRequired},
		{IssueVSCodeIntegration, ExitVSCodeIntegrationFailure},
		{IssueVSCodeExtension, ExitVSCodeIntegrationFailure},
		{IssueVSCodeAliasUnavailable, ExitManualRequired},
		{IssueVSCodeTrust, ExitManualRequired},
		{IssueVSCodeToolLimit, ExitManualRequired},
		{IssueVSCodeModelSelection, ExitManualRequired},
		{IssueCopilotMissing, ExitPrerequisiteFailure},
		{IssueCopilotVersion, ExitPrerequisiteFailure},
		{IssueCopilotLauncher, ExitPrerequisiteFailure},
		{IssueCopilotProvider, ExitPrerequisiteFailure},
		{IssueCopilotAlias, ExitRuntimeVerificationFailure},
		{IssueCopilotOllama, ExitRuntimeVerificationFailure},
		{IssueStateFailure, ExitStateOrTransactionFailure},
		{IssueStateSchema, ExitStateOrTransactionFailure},
		{IssueStateResume, ExitStateOrTransactionFailure},
		{IssueStateWrite, ExitStateOrTransactionFailure},
		{IssueConfigurationInvalid, ExitConfigurationFailure},
		{IssueConfigurationConflict, ExitConfigurationFailure},
		{IssueAssetOwnership, ExitStateOrTransactionFailure},
		{IssueAssetConfirmation, ExitManualRequired},
		{IssueUpdateUnavailable, ExitIntegrityFailure},
		{IssueUpdateSelfCheck, ExitInternalFailure},
		{IssueInternal, ExitInternalFailure},
		{"LAI-UNKNOWN-999", ExitInternalFailure},
	}

	for _, test := range tests {
		t.Run(string(test.code), func(t *testing.T) {
			if got := ExitForIssue(test.code); got != test.want {
				t.Fatalf("ExitForIssue(%q) = %d, want %d", test.code, got, test.want)
			}
		})
	}
}
