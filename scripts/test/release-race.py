"""Bounded, serial release race checks; the full ordinary Go suite runs separately."""
import os
import subprocess

CASES = {
    './internal/store': [
        'TestPrivateSnapshotAfterAuthorizationAndCancellation',
        'TestHealthCompletionWatcherCancellationCannotLeaveFinishing',
        'TestHealthOldCompletionCannotUnlockNewGeneration',
        'TestCleanupBatchRollbackMissingAndGeneration',
        'TestCleanupBatchBoundAndCancellation',
        'TestReadingConcurrentSessionRetryAndWriteEpoch',
        'TestReadingCancelledWriteThenImmediateRetryAndDeviceRace',
        'TestAuthorizationConnectionReplacementAndOldNewWriteScope',
        'TestRecommendationCancelImmediateNewJobRejectsOldResult',
        'TestIncrementalDiscoveryServiceReusesAllPairsAndOnlyChangedEdition',
        'TestDiscoveryRebuildsLostWorkerPoolWithoutRepeatingCommittedRows',
        'TestDiscoveryRepeatedWorkerFailurePausesAndPartialCheckpointCanBeReused',
        'TestSeriesEvidenceGenerationPinsBaselineAndRejectsStaleWrites',
        'TestImportCancellationKeepsPublishedDataAndRejectsOldGeneration',
    ],
    './internal/maintenance': [
        'TestCancelThenNewMaintenanceWaitsForOldIO',
        'TestInstanceUploadFinalResultAndCancellationDoNotDemoteNewStates',
        'TestInstanceUploadFailureCannotOverwriteAbandonedOrNewPendingGeneration',
        'TestHealthIOBarrierPausesTasksAndRejectsOldPublish',
    ],
    './internal/library': ['TestCancellationImmediateRestartAndStalePublish'],
    './internal/httpapi': ['TestCatalogHTTPFlow', 'TestCatalogOpenAPIContract'],
    './internal/app': ['TestOfflineRehashUpgradeLockRestartAndOfflineCover'],
    './internal/recommendation': None,
}


def main():
    go = os.environ.get('GO', 'go')
    for package, names in CASES.items():
        pattern = '^(' + '|'.join(names) + ')$' if names else '^Test'
        listed = subprocess.check_output([go, 'test', package, '-list', pattern], text=True)
        actual = {line for line in listed.splitlines() if line.startswith('Test')}
        if not actual or (names and actual != set(names)):
            raise SystemExit(f'race selection missing tests: {package}')
        print(f'Race {package}: {len(actual)} tests, one package at a time', flush=True)
        subprocess.run([go, 'test', '-race', '-p', '1', '-timeout', '20m', '-count=1',
                        '-v', package, '-run', pattern], check=True)


if __name__ == '__main__':
    main()
