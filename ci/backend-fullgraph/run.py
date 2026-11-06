#!/usr/bin/env python3
"""Fresh owned graph; no old product binary, runtime result, DB or external deployment is adopted."""
import argparse,hashlib,importlib.util,json,os,re,stat,subprocess,sys,time,shutil
from pathlib import Path
from collections import Counter
REPO=Path(__file__).resolve().parents[2]
INPUTS=Path(__file__).with_name('inputs.json')
FINAL_SOURCE = {'members': 1576, 'digest': 'f2de06c0b25c49735db974b9a726adbfae66612a4b3f35962b9e1a73207d3e6f'}
SOURCE_ROOT='ci/candidates/backend-fullgraph/source/'
REQUIRED_GRAPH_SOURCE={'server/internal/runtimeacceptance/acceptance_linux.go','server/internal/runtimeacceptance/graph_linux.go','server/internal/runtimeacceptance/cancellation_linux.go','server/cmd/tools/owned-fullgraph-acceptance/main.go','server/pkg/common/startrpc/fullgraph_evidence_linux_test.go'}
RESOURCE={'initialBytes':4<<30,'floorMiB':1024,'allocatedMiB':16384,'pollSeconds':.2,'controllerSeconds':10800,'mainCompileTotalSeconds':1200,'scenarioSeconds':1380,'originalOperatorContextSeconds':1200,'targetGoTimeoutSeconds':180}
SCENARIOS=('reader-registered-cancel','reader-writer-fence-cancel','ack','redis-unknown')
ROLES=('api','crontask','msggateway','msgtransfer-managed','push','rpc-auth','rpc-conversation','rpc-encryption','rpc-friend','rpc-group','rpc-msg','rpc-rtc','rpc-third','rpc-user')
CLEANUP_SCOPES=[{'label': 'cleanup-ownedcontrol', 'targets': {'internal/namespaceimport/ownedcontrol': ['TestOwnedCleanupSharesSingleSignalAcrossOriginalStopPaths']}, 'requiredSubtests': {'internal/namespaceimport/ownedcontrol': {'TestOwnedCleanupSharesSingleSignalAcrossOriginalStopPaths': ['local-first']}}}, {'label': 'cleanup-runtimeacceptance', 'targets': {'internal/runtimeacceptance': ['TestFullgraphEarlyFailureKeepsOriginalChildrenForFinalCleanup']}, 'requiredSubtests': {'internal/runtimeacceptance': {'TestFullgraphEarlyFailureKeepsOriginalChildrenForFinalCleanup': ['cancel-audit-before-stop', 'timeout-before-local-wait-complete']}}}]
CLEANUP_PASSED_REUSE={'run': 39062533742, 'job': 117483277540, 'head': '756f82e2b90310020e197ee7848eb34c50c40766', 'source': {'members': 1575, 'digest': '57513d328ed76c6a1091d8caee531b7a6d5d952982f9621849369aa7e46e6bd6'}, 'reviewSHA256': '01060ed44de69cd19b42b645e384a99a6dd2f0ddcfb7aa8ec434b18ca90257cf', 'artifact': {'artifact': 12112670650, 'run': 39062533742, 'attempt': 1, 'head': '756f82e2b90310020e197ee7848eb34c50c40766', 'sha256': '3eb9a5a1a223210c9222c712a8de4074b03d5606dd72137aaf07b891a29c6581', 'bytes': 54487, 'exactAPIDigest': True, 'files': 69, 'binariesDownloaded': False}, 'topPASS': 2, 'subPASS': 3, 'namedCompletions': [{'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedCleanupSharesSingleSignalAcrossOriginalStopPaths'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedCleanupSharesSingleSignalAcrossOriginalStopPaths/local-first'}, {'package': 'internal/runtimeacceptance', 'name': 'TestFullgraphEarlyFailureKeepsOriginalChildrenForFinalCleanup'}, {'package': 'internal/runtimeacceptance', 'name': 'TestFullgraphEarlyFailureKeepsOriginalChildrenForFinalCleanup/cancel-audit-before-stop'}, {'package': 'internal/runtimeacceptance', 'name': 'TestFullgraphEarlyFailureKeepsOriginalChildrenForFinalCleanup/timeout-before-local-wait-complete'}], 'sourceToolsPostExact': True, 'runtimeAccepted': False}
CLEANUP_COMPLETE_SCOPES=[{'label': 'cleanup-ownedcontrol', 'targets': {'internal/namespaceimport/ownedcontrol': ['TestNamespaceRuntimeStopKeepsEveryOriginalChildOwnedAfterOneNoncanonicalExit', 'TestOwnedCleanupSharesSingleSignalAcrossOriginalStopPaths', 'TestOwnedProcessImageStreamingKeepsOriginalIdentityAndCancellation', 'TestOwnedLocalCleanupCancelledValidationAndOriginalDoneStayLocal']}, 'requiredSubtests': {'internal/namespaceimport/ownedcontrol': {'TestNamespaceRuntimeStopKeepsEveryOriginalChildOwnedAfterOneNoncanonicalExit': ['unknown-middle-child-does-not-strand-later-original-owner', 'good-drains-retained-and-unknown-never-gets-a-join', 'repeat-Stop-does-not-renew-signal-or-erase-failure'], 'TestOwnedCleanupSharesSingleSignalAcrossOriginalStopPaths': ['canonical-first', 'runtime-first', 'local-first'], 'TestOwnedProcessImageStreamingKeepsOriginalIdentityAndCancellation': ['actual-original-image-and-bounded-file-pins', 'real-fd-path-stability-and-mid-read-cancellation'], 'TestOwnedLocalCleanupCancelledValidationAndOriginalDoneStayLocal': ['cancelled-validation-keeps-live-child-unsignalled', 'same-original-wait-during-image-validation-is-local-only']}}}, {'label': 'cleanup-runtimeacceptance', 'targets': {'internal/runtimeacceptance': ['TestFullgraphEarlyFailureKeepsOriginalChildrenForFinalCleanup']}, 'requiredSubtests': {'internal/runtimeacceptance': {'TestFullgraphEarlyFailureKeepsOriginalChildrenForFinalCleanup': ['cancel-audit-before-stop', 'timeout-before-local-wait-complete']}}}]
CLEANUP_RECENT_REUSE={'run': 39060706865, 'job': 117477900963, 'head': 'f48bd32eeea5b31568fe2e2e03ea56a47c655264', 'source': {'members': 1575, 'digest': '52ddd8742eef822c04d14b99f6ccf47acd12f43c7df06d98fe6fe389da231b58'}, 'reviewSHA256': '41a7f45b3323effb1529bf6e417bbe31cd53cc41fbe354f260cccc335d346794', 'artifact': {'artifact': 12111770451, 'run': 39060706865, 'attempt': 1, 'head': 'f48bd32eeea5b31568fe2e2e03ea56a47c655264', 'sha256': '483cdf79808816f7778eaedb27378e09d85b903370964f66ab083d75b4e8e714', 'bytes': 9702, 'exactAPIDigest': True, 'files': 12, 'binariesDownloaded': False}, 'topPASS': 3, 'subPASS': 9, 'namedCompletions': [{'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestNamespaceRuntimeStopKeepsEveryOriginalChildOwnedAfterOneNoncanonicalExit/unknown-middle-child-does-not-strand-later-original-owner'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestNamespaceRuntimeStopKeepsEveryOriginalChildOwnedAfterOneNoncanonicalExit/good-drains-retained-and-unknown-never-gets-a-join'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestNamespaceRuntimeStopKeepsEveryOriginalChildOwnedAfterOneNoncanonicalExit/repeat-Stop-does-not-renew-signal-or-erase-failure'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestNamespaceRuntimeStopKeepsEveryOriginalChildOwnedAfterOneNoncanonicalExit'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedProcessImageStreamingKeepsOriginalIdentityAndCancellation/actual-original-image-and-bounded-file-pins'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedProcessImageStreamingKeepsOriginalIdentityAndCancellation/real-fd-path-stability-and-mid-read-cancellation'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedProcessImageStreamingKeepsOriginalIdentityAndCancellation'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedLocalCleanupCancelledValidationAndOriginalDoneStayLocal/cancelled-validation-keeps-live-child-unsignalled'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedLocalCleanupCancelledValidationAndOriginalDoneStayLocal/same-original-wait-during-image-validation-is-local-only'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedLocalCleanupCancelledValidationAndOriginalDoneStayLocal'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedCleanupSharesSingleSignalAcrossOriginalStopPaths/canonical-first'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedCleanupSharesSingleSignalAcrossOriginalStopPaths/runtime-first'}], 'partialChildParent': {'TestOwnedCleanupSharesSingleSignalAcrossOriginalStopPaths': ['canonical-first', 'runtime-first']}, 'oldParentFAILNotReused': True, 'sourceAndToolsPostExact': True}
CLEANUP_FIXTURE_REPAIR={'target': 'server/internal/namespaceimport/ownedcontrol/process_local_cleanup_linux_test.go', 'beforeSHA256': '4f40dfe1837207983cf907d3ed161e6c6c1c3493cec39c557512d1d5bde9cedd', 'afterSHA256': 'bcf11c0283992a27812fa20666dad5d7ecb95d514e5bb0e0ceee7816279030b8', 'bytes': 6777, 'productionChanges': 0}
CLEANUP_REUSE={'run': 39053868483, 'head': 'ce7964a0c7aa474cbc83c32dd71c1bfcfb83cfec', 'source': {'members': 1573, 'digest': 'f6f1568c3dbc12b36441108f36bb771153f134fd5907a056a5532b0ee773d7f2'}, 'reviewSHA256': '48c619c7b7c19ad33b5bb5b9bfa38f655de918c01ad19a6784fed4d9ccc1efbf', 'package': 'internal/namespaceimport/ownedcontrol', 'subtests': {'TestOwnedCleanupSharesSingleSignalAcrossOriginalStopPaths': ['unbound_start_failure_only_waits_original_done']}}
LAUNCHER_PASSED_REUSE={'run': 39075035152, 'job': 117520306290, 'head': 'a6d9276c53a75e8ca19e351cab1d34b419b560e7', 'source': {'members': 1575, 'digest': '868e46b88717f80dd90e837ebd5bb64634ed5030ddd3c83a8d6c00398c749717'}, 'reviewSHA256': '412fdacae0a1a8ff842d92269d2bf00fb0a578b3ed1eab928ee86d2e56ac1f38', 'artifact': {'artifact': 12115839677, 'run': 39075035152, 'attempt': 1, 'head': 'a6d9276c53a75e8ca19e351cab1d34b419b560e7', 'sha256': '674eadc00436aa018a1fe7828be0568f3ce80f499ef53b487deacfcb5709099a', 'bytes': 52320, 'exactAPIDigest': True, 'files': 66, 'binariesDownloaded': False}, 'launcherNamesSHA256': '6471ec2f43ce64c92e2c399c7a0630e1d86740c9e2ee2a69352573e14b61c932', 'topPASS': 1, 'subPASS': 0, 'skip': 0, 'fail': 0, 'namedCompletions': [{'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedLauncherHoldPersistsAndCannotPrepareMetadata'}], 'sourceToolsPostExact': True, 'currentSourcePASS': False, 'runtimeAccepted': False}
GRAPH_TARGETS={'pkg/common/startrpc': ['TestOwnedPreparedReaderRegistersBeforeServeAndRetainsFinalWriterFence', 'TestOwnedPushActualOwnerClosesOriginalResourcesBeforeRedisUnknownNonpermit']}
REDIS_PARSER_CONTROL = {'phase': 'redis-parser', 'targets': {'internal/namespaceimport/ownedcontrol': ['TestOwnedRedisCredentialNormalizesRESP2RESP3WithoutWeakeningDisableProof']}, 'requiredSubtests': {'internal/namespaceimport/ownedcontrol': {'TestOwnedRedisCredentialNormalizesRESP2RESP3WithoutWeakeningDisableProof': ['equivalent-protocols-complete-summary-and-real-disabled-state', 'malformed-fields-and-nil-first-duplicate-reject', 'disabled-check-keeps-off-allcommands-and-empty-passwords']}}, 'goTimeoutSeconds': 180, 'phaseGuardSeconds': 240, 'newDatabaseFixtures': 0, 'otherR8TargetsDispatched': False}
REDIS_PARSER_PASSED_REUSE = {'run': 39082704756, 'job': 117542803080, 'attempt': 1, 'head': '2f17d99d2f32c4fc72b9b922f8d2a8783938b665', 'source': {'members': 1576, 'digest': '122c6e2d1b158bbe9addd879c733ef85bf6c2b26a345f583aa4b5a31d2260f48'}, 'artifact': {'artifact': 12119877247, 'run': 39082704756, 'attempt': 1, 'head': '2f17d99d2f32c4fc72b9b922f8d2a8783938b665', 'sha256': 'b7bbb052d28081550d537cfb867103cf8e2e19ad84f4b90893a4157f569f7eef', 'bytes': 41738, 'exactAPIDigest': True, 'files': 56, 'binariesDownloaded': False}, 'rootReviewSHA256': 'cc2fa43abea21d6b2c305571e66eeb62e422f2a67f5965b6c71591f532c40501', 'topPASS': 1, 'subPASS': 3, 'skip': 0, 'fail': 0, 'namedCompletions': [{'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedRedisCredentialNormalizesRESP2RESP3WithoutWeakeningDisableProof'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedRedisCredentialNormalizesRESP2RESP3WithoutWeakeningDisableProof/disabled-check-keeps-off-allcommands-and-empty-passwords'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedRedisCredentialNormalizesRESP2RESP3WithoutWeakeningDisableProof/equivalent-protocols-complete-summary-and-real-disabled-state'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedRedisCredentialNormalizesRESP2RESP3WithoutWeakeningDisableProof/malformed-fields-and-nil-first-duplicate-reject'}], 'sourcePins': [{'target': 'server/internal/namespaceimport/ownedcontrol/redis_credential.go', 'bytes': 6107, 'sha256': '26fad310f6cda0b25068cb03e6ab53d2a59a5258f78923b8ab7f49db5a5461b6'}, {'target': 'server/internal/namespaceimport/ownedcontrol/redis_credential_test.go', 'bytes': 5522, 'sha256': 'd9c8471dd01df8dcd9b746edb179a751dbf6444d756eab83d5eea4e3e731867d'}], 'sourcePostExact': True, 'currentSourcePASS': False, 'runtimeAccepted': False, 'actualRedisParserTestsDispatched': 0, 'rawEvidence': [{'path': '.qa/backend-reader-boundary-and-redis-golden-batch-1/graph-artifact/redis-parser-names.json', 'bytes': 956, 'sha256': '94e60e18529a5f933ce070d15e0418819d19ed725929cc4e4cb8c5474bcf1c13'}, {'path': '.qa/backend-reader-boundary-and-redis-golden-batch-1/graph-artifact/redis-parser-events.json', 'bytes': 5001, 'sha256': '1056eb00f05ca35635dca33dd667d538f1f967a329509af0ecec1ba5d9e0afd7'}, {'path': '.qa/backend-reader-boundary-and-redis-golden-batch-1/graph-artifact/source-post.json', 'bytes': 120, 'sha256': 'd25ccb64f558e0966dbdacea71ba478c3f50330e0d3583f611165ece87aed8cc'}, {'path': '.qa/backend-reader-boundary-and-redis-golden-batch-1/graph-artifact/phase-0004.json', 'bytes': 2509, 'sha256': 'b3594ce1d3614e16a7b91e95982b3b4aff0fc5bb6005ea33ce5167eeb423628d'}]}
KAFKA_ACL_CONTROL = {'phase': 'kafka-acl-response', 'targets': {'internal/namespaceimport/ownedcontrol': ['TestOwnedKafkaExclusionRequiresEveryWildcardDeny']}, 'requiredSubtests': {'internal/namespaceimport/ownedcontrol': {'TestOwnedKafkaExclusionRequiresEveryWildcardDeny': ['create-reply-error-and-shape-cannot-be-a-success', 'describe-reply-error-and-shape-cannot-replace-exact-deny']}}, 'goTimeoutSeconds': 180, 'phaseGuardSeconds': 240, 'newDatabaseFixtures': 0, 'otherKafkaOrRedisTestsDispatched': False}
GRAPH_CHILDREN={'TestOwnedPreparedReaderRegistersBeforeServeAndRetainsFinalWriterFence': ['registered-not-serving-before-readiness', 'known-readiness-still-keeps-writer-fence'], 'TestOwnedPushActualOwnerClosesOriginalResourcesBeforeRedisUnknownNonpermit': ['required-token-only']}


KAFKA_ACL_PASSED_REUSE = {'schema': 1, 'kind': 'root-accepted-kafka-acl-source-bound-reuse-v1', 'run': 39091570750, 'job': 117569259635, 'attempt': 1, 'head': '500baca44c853079ca6f06a001ec3a2bf873cf23', 'source': {'members': 1576, 'digest': 'a41e244f1126427cee7b374532b4e382fa7e2d08640d01c89f29f62fccbd0371'}, 'artifact': {'artifact': 12122902662, 'run': 39091570750, 'attempt': 1, 'head': '500baca44c853079ca6f06a001ec3a2bf873cf23', 'sha256': '7ed1b07f467037c4a148cc5de413232f161101af65b5eba8cde53d638fbd07d4', 'bytes': 44383, 'exactAPIDigest': True, 'files': 59, 'binariesDownloaded': False}, 'rootReviewSHA256': '308412b715bdf1bc5b3a60e9abd2b4122d7e3a082353af444ae1c94a93110a02', 'compactCustody': {'bytes': 7234, 'sha256': '1f227f154cda907d61e8b48ce6253a3d5032aff001823414da3621a5cd5370f9'}, 'events': {'bytes': 3537, 'sha256': '300357fa4c07a8c840f538cb5ed0422bcc84fb5e888f98b0a6366f5e539bbd59', 'body': [{'Time': '2026-10-18T02:54:25.312427428Z', 'Action': 'start', 'Package': 'github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol'}, {'Time': '2026-10-18T02:54:25.339692637Z', 'Action': 'run', 'Package': 'github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol', 'Test': 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny'}, {'Time': '2026-10-18T02:54:25.339752753Z', 'Action': 'output', 'Package': 'github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol', 'Test': 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny'}, {'Time': '2026-10-18T02:54:25.339777823Z', 'Action': 'run', 'Package': 'github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol', 'Test': 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny/create-reply-error-and-shape-cannot-be-a-success'}, {'Time': '2026-10-18T02:54:25.339781401Z', 'Action': 'output', 'Package': 'github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol', 'Test': 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny/create-reply-error-and-shape-cannot-be-a-success'}, {'Time': '2026-10-18T02:54:25.339909226Z', 'Action': 'output', 'Package': 'github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol', 'Test': 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny/create-reply-error-and-shape-cannot-be-a-success'}, {'Time': '2026-10-18T02:54:25.339922815Z', 'Action': 'pass', 'Package': 'github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol', 'Test': 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny/create-reply-error-and-shape-cannot-be-a-success', 'Elapsed': 0}, {'Time': '2026-10-18T02:54:25.339959837Z', 'Action': 'run', 'Package': 'github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol', 'Test': 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny/describe-reply-error-and-shape-cannot-replace-exact-deny'}, {'Time': '2026-10-18T02:54:25.339962892Z', 'Action': 'output', 'Package': 'github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol', 'Test': 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny/describe-reply-error-and-shape-cannot-replace-exact-deny'}, {'Time': '2026-10-18T02:54:25.34002031Z', 'Action': 'output', 'Package': 'github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol', 'Test': 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny/describe-reply-error-and-shape-cannot-replace-exact-deny'}, {'Time': '2026-10-18T02:54:25.3400262Z', 'Action': 'pass', 'Package': 'github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol', 'Test': 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny/describe-reply-error-and-shape-cannot-replace-exact-deny', 'Elapsed': 0}, {'Time': '2026-10-18T02:54:25.340075932Z', 'Action': 'output', 'Package': 'github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol', 'Test': 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny'}, {'Time': '2026-10-18T02:54:25.340100894Z', 'Action': 'pass', 'Package': 'github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol', 'Test': 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny', 'Elapsed': 0}, {'Time': '2026-10-18T02:54:25.340196573Z', 'Action': 'output', 'Package': 'github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol'}, {'Time': '2026-10-18T02:54:26.341379184Z', 'Action': 'output', 'Package': 'github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol'}, {'Time': '2026-10-18T02:54:26.341443904Z', 'Action': 'pass', 'Package': 'github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol', 'Elapsed': 1.029}]}, 'names': {'bytes': 697, 'sha256': 'a8fffe53ed002f65add20e337716c2c3b56cf3c30dbc44ac0640f5de1709f701', 'body': {'topPASS': 1, 'subPASS': 2, 'skip': 0, 'fail': 0, 'packages': 1, 'namedCompletions': [{'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny/create-reply-error-and-shape-cannot-be-a-success'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny/describe-reply-error-and-shape-cannot-replace-exact-deny'}], 'source': {'members': 1576, 'digest': 'a41e244f1126427cee7b374532b4e382fa7e2d08640d01c89f29f62fccbd0371'}, 'accepted': True, 'newDatabaseFixtures': 0, 'otherKafkaOrRedisTestsDispatched': False}}, 'sourcePost': {'bytes': 120, 'sha256': 'ffb5c0c82e5318493612eaf60150fdd2ed6ec6d5856a9e259bb20b5390671a2d', 'body': {'accepted': True, 'source': {'members': 1576, 'digest': 'a41e244f1126427cee7b374532b4e382fa7e2d08640d01c89f29f62fccbd0371'}}}, 'toolsBefore': {'bytes': 1277, 'sha256': 'c524b97c60b95cb63416089a921e4459e3738ca77cc04730dcf52c3cd501b740'}, 'toolsAfter': {'bytes': 1277, 'sha256': 'c524b97c60b95cb63416089a921e4459e3738ca77cc04730dcf52c3cd501b740'}, 'goTool': {'bytes': 17164055, 'sha256': '548d3c32f83fa81152f543eb1526be7884cfe0526eb5e9ce29d3eca14786e7e0', 'mode': 493}, 'historicalTopPASS': 1, 'historicalSubPASS': 2, 'actualCurrentTestsDispatched': 0, 'sourceBoundReuse': True, 'currentFreshPASS': False, 'runtimeAccepted': False}

def sha(raw):return hashlib.sha256(raw).hexdigest()
def regular(p,maximum=128<<20):
 p=Path(p)
 if p.resolve()!=p:raise ValueError('canonical bounded regular file required')
 fd=os.open(p,os.O_RDONLY|os.O_NOFOLLOW)
 try:
  before=os.fstat(fd)
  if not stat.S_ISREG(before.st_mode) or before.st_size>maximum:raise ValueError('bounded regular file required')
  chunks=[];left=maximum+1
  while left:
   block=os.read(fd,min(1<<20,left))
   if not block:break
   chunks.append(block);left-=len(block)
  raw=b''.join(chunks);after=os.fstat(fd);current=p.lstat()
  keys=lambda v:(v.st_dev,v.st_ino,v.st_mode,v.st_size,v.st_mtime_ns,v.st_ctime_ns)
  if keys(before)!=keys(after) or keys(before)!=keys(current) or len(raw)!=before.st_size:raise ValueError('file body/mode changed while reading')
  return raw
 finally:os.close(fd)

def pin(row):
 p=REPO/row['path'];raw=regular(p)
 if len(raw)!=row['bytes'] or sha(raw)!=row['sha256'] or stat.S_IMODE(p.stat().st_mode)!=0o644:raise ValueError('pinned input differs')
 return raw
def module(path,label):
 s=importlib.util.spec_from_file_location(label,REPO/path);v=importlib.util.module_from_spec(s);s.loader.exec_module(v);return v
def write(path,value):
 raw=(json.dumps(value,separators=(',',':'),ensure_ascii=False)+'\n').encode()
 fd=os.open(path,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600)
 with os.fdopen(fd,'wb') as f:
  if f.write(raw)!=len(raw):raise ValueError('short metadata write')
  f.flush();os.fsync(f.fileno())
 fd=os.open(path.parent,os.O_RDONLY|os.O_DIRECTORY)
 try:os.fsync(fd)
 finally:os.close(fd)
 return sha(raw)
CURRENT_IMAGE_KAFKA_CONTROL = {'phase':'current-image-kafka','targets':{'internal/namespaceimport/ownedcontrol':['TestOwnedKafkaExclusionRequiresEveryWildcardDeny','TestOwnedProcessImageStreamingKeepsOriginalIdentityAndCancellation']},'requiredSubtests':{'internal/namespaceimport/ownedcontrol':{'TestOwnedKafkaExclusionRequiresEveryWildcardDeny':['create-reply-error-and-shape-cannot-be-a-success','describe-reply-error-and-shape-cannot-replace-exact-deny'],'TestOwnedProcessImageStreamingKeepsOriginalIdentityAndCancellation':['actual-original-image-and-bounded-file-pins','real-fd-path-stability-and-mid-read-cancellation']}},'goTimeoutSeconds':180,'phaseGuardSeconds':240,'topPASS':2,'subPASS':4,'samePackageSingleRaceInvocation':True,'newDatabaseFixtures':0}

def check_current_image_kafka_plan(c):
 if c.get('currentKafkaConfirmControl') is not None or c.get('kafkaConfirmPassedReuse') is not None or c.get('currentReleaseControl') is not None or c.get('currentManagedShapeControl') is not None:raise ValueError('only one explicit current scope is allowed')
 expected=next(iter(KAFKA_ACL_CONTROL['targets'].values()))
 image=['TestOwnedProcessImageStreamingKeepsOriginalIdentityAndCancellation']
 if c.get('kafkaACLTestsToRun')!=expected or c.get('processImageTestsToRun')!=image or c.get('currentImageKafkaControl')!=CURRENT_IMAGE_KAFKA_CONTROL or c.get('kafkaACLPassedReuse') is not None:raise ValueError('exact fresh current image and Kafka scope required')
 check_kafka_acl_receipt(c.get('kafkaACLHistoricalPass'))
 return CURRENT_IMAGE_KAFKA_CONTROL

def check_current_image_kafka_receipt(value):
 control=CURRENT_IMAGE_KAFKA_CONTROL;targets=control['targets'];package=next(iter(targets))
 keys={'topPASS','subPASS','skip','fail','packages','namedCompletions','source','accepted','newDatabaseFixtures','scope','actualKafkaACLTestsDispatched','actualProcessImageTestsDispatched','samePackageSingleRaceInvocation','runtimeAccepted','wholeSourceAccepted'}
 if not isinstance(value,dict) or set(value)!=keys or value['source']!=FINAL_SOURCE or value['accepted'] is not True or value['runtimeAccepted'] is not False or value['wholeSourceAccepted'] is not False or value['scope']!=control or value['samePackageSingleRaceInvocation'] is not True:raise ValueError('exact current source-bound image/Kafka receipt required')
 for name,want in [('topPASS',2),('subPASS',4),('skip',0),('fail',0),('packages',1),('newDatabaseFixtures',0),('actualKafkaACLTestsDispatched',1),('actualProcessImageTestsDispatched',1)]:
  if type(value[name]) is not int or value[name]!=want:raise ValueError('exact fresh current image/Kafka counts required')
 expected={(package,top) for top in targets[package]}
 expected|={(package,top+'/'+child) for top,children in control['requiredSubtests'][package].items() for child in children}
 names=[(row['package'],row['name']) for row in value['namedCompletions']]
 if len(names)!=6 or set(names)!=expected:raise ValueError('every current parent and original child required')
 return value

def run_current_image_kafka_control(go,environment,command,parent,out):
 control=CURRENT_IMAGE_KAFKA_CONTROL;targets=control['targets'];package=next(iter(targets));pattern='^('+'|'.join(re.escape(top) for top in targets[package])+')$'
 raw=command(control['phase'],[go,'test','-mod=readonly','-race','-p=1','-parallel=1','-count=1','-json','-vet=off','-timeout=180s','-run',pattern,'./'+package],control['phaseGuardSeconds'],environment)
 events=[json.loads(line) for line in raw.decode().splitlines() if line.strip()]
 result=parent.audit(events,0,targets)
 receipt=dict(result,source=FINAL_SOURCE,accepted=True,newDatabaseFixtures=0,scope=control,actualKafkaACLTestsDispatched=1,actualProcessImageTestsDispatched=1,samePackageSingleRaceInvocation=True,runtimeAccepted=False,wholeSourceAccepted=False)
 check_current_image_kafka_receipt(receipt)
 write(out/'current-image-kafka-names.json',receipt)
 write(out/'kafka-acl-historical-pass.json',check_kafka_acl_receipt(dict(KAFKA_ACL_PASSED_REUSE)))
 return receipt

def write_current_image_kafka_summary(out,receipt,cleanup_audit,launcher_reuse,redis_parser_reuse):
 check_current_image_kafka_receipt(receipt)
 write(out/'summary.json',{'accepted':True,'source':FINAL_SOURCE,'actualCurrentMainBuilds':14,'auxiliaryBuilds':2,'freshGraphScenarios':4,'pendingOriginalTopPASS':2,'pendingOriginalSubPASS':3,'cleanupTopPASS':0,'cleanupSubPASS':0,'cleanupPassedReuse':CLEANUP_PASSED_REUSE,'launcherTopPASS':0,'launcherSubPASS':0,'launcherPassedReuse':LAUNCHER_PASSED_REUSE,'actualLauncherTestsDispatched':0,'targetedTopPASS':2,'targetedSubPASS':3,'kafkaACLTopPASS':1,'kafkaACLSubPASS':2,'actualKafkaACLTestsDispatched':1,'processImageTopPASS':1,'processImageSubPASS':2,'actualProcessImageTestsDispatched':1,'currentImageKafkaScope':receipt,'kafkaACLPassedReuse':None,'kafkaACLHistoricalPass':KAFKA_ACL_PASSED_REUSE,'redisParserTopPASS':0,'redisParserSubPASS':0,'redisParserHistoricalTopPASS':1,'redisParserHistoricalSubPASS':3,'actualRedisParserTestsDispatched':0,'redisParserPassedReuse':redis_parser_reuse,'cleanupReuse':dict(CLEANUP_REUSE,topPASS=0,subPASS=1),'cleanupRecentReuse':CLEANUP_RECENT_REUSE,'historicalCleanupSourceFactsOnly':True,'currentWholeOwnedcontrolPackagePASS':False,'originalObligation':{'topCount':5,'subCount':12,'crossSource':True},'newSafety':{'topCount':2,'subCount':4,'crossSource':True},'completeObligation':{'topCount':7,'subCount':16,'crossSource':True},'old67or21TestsRerun':0,'reuseOldBinary':False,'deployment':False,'resourceCleanup':'must independently pass always collector','privateLogsUploaded':False})

CURRENT_KAFKA_CONFIRM_CONTROL = {'phase':'current-kafka-confirm','targets':{'internal/namespaceimport/ownedcontrol':['TestOwnedKafkaExclusionRequiresEveryWildcardDeny']},'requiredSubtests':{'internal/namespaceimport/ownedcontrol':{'TestOwnedKafkaExclusionRequiresEveryWildcardDeny':['create-reply-error-and-shape-cannot-be-a-success','describe-reply-error-and-shape-cannot-replace-exact-deny','new-deny-visibility-is-bounded-without-grace-for-existing-denies']}},'goTimeoutSeconds':180,'phaseGuardSeconds':240,'topPASS':1,'subPASS':3,'samePackageSingleRaceInvocation':True,'newDatabaseFixtures':0}
PROCESS_IMAGE_PRIOR_PASS = {'run': 42180539532, 'job': 127768872346, 'head': '3942c2acadd0b6343b9f64e3a444a52d0a28e3d7', 'attempt': 1, 'source': {'members': 1576, 'digest': 'eaf61802fc86b1ff65a08bb8b75d64a2d8813d9af1bf54d41bf2aa37b9f142af'}, 'rootReviewSHA256': 'eebbe9f2a571f467390d017ead3abc7f80174356c7cba75ee25592fda8390de8', 'originalScope': 'current-image-kafka', 'topPASS': 1, 'subPASS': 2, 'skip': 0, 'fail': 0, 'namedCompletions': [{'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedProcessImageStreamingKeepsOriginalIdentityAndCancellation'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedProcessImageStreamingKeepsOriginalIdentityAndCancellation/actual-original-image-and-bounded-file-pins'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedProcessImageStreamingKeepsOriginalIdentityAndCancellation/real-fd-path-stability-and-mid-read-cancellation'}], 'sourcePins': [{'target': 'server/internal/namespaceimport/ownedcontrol/process_linux.go', 'bytes': 28188, 'sha256': 'd413c338a3b42786f206f1575e570c390a8814a9c702a1b87c5eed2a06f9eee9'}, {'target': 'server/internal/namespaceimport/ownedcontrol/process_image_stream_linux_test.go', 'bytes': 3908, 'sha256': '80b653eb9cf2a943bc70da2eb81751891ceade8a6258ee3b16fd9e609d79217e'}], 'sourceToolsPostExact': True, 'currentSourcePASS': False, 'currentPackagePASS': False, 'actualCurrentTestsDispatched': 0, 'runtimeAccepted': False}

def check_process_image_prior(value,bodies=None):
 if json.dumps(value,sort_keys=True,separators=(',',':'))!=json.dumps(PROCESS_IMAGE_PRIOR_PASS,sort_keys=True,separators=(',',':')) or value['source']==FINAL_SOURCE:raise ValueError('exact prior image fact without current source/package acceptance required')
 if bodies is not None:
  for row in value['sourcePins']:
   body=bodies.get(row['target'])
   if body is None or len(body)!=row['bytes'] or sha(body)!=row['sha256']:raise ValueError('unchanged original process image bodies required')
 return value

def check_current_kafka_confirm_plan(c):
 if c.get('kafkaConfirmPassedReuse') is not None or c.get('currentReleaseControl') is not None or c.get('currentManagedShapeControl') is not None:raise ValueError('fresh Kafka confirmation cannot alias other current scopes')
 expected=next(iter(CURRENT_KAFKA_CONFIRM_CONTROL['targets'].values()))
 if c.get('kafkaACLTestsToRun')!=expected or c.get('processImageTestsToRun')!=[] or c.get('currentImageKafkaControl') is not None or c.get('currentKafkaConfirmControl')!=CURRENT_KAFKA_CONFIRM_CONTROL or c.get('kafkaACLPassedReuse') is not None:raise ValueError('exact current Kafka confirmation scope without image redispatch required')
 check_kafka_acl_receipt(c.get('kafkaACLHistoricalPass'));check_process_image_prior(c.get('processImageHistoricalPass'))
 return CURRENT_KAFKA_CONFIRM_CONTROL

def check_current_kafka_confirm_receipt(value):
 control=CURRENT_KAFKA_CONFIRM_CONTROL;targets=control['targets'];package=next(iter(targets))
 keys={'topPASS','subPASS','skip','fail','packages','namedCompletions','source','accepted','newDatabaseFixtures','scope','actualKafkaACLTestsDispatched','actualProcessImageTestsDispatched','samePackageSingleRaceInvocation','runtimeAccepted','wholeSourceAccepted'}
 if not isinstance(value,dict) or set(value)!=keys or value['source']!=FINAL_SOURCE or value['accepted'] is not True or value['runtimeAccepted'] is not False or value['wholeSourceAccepted'] is not False or value['scope']!=control or value['samePackageSingleRaceInvocation'] is not True:raise ValueError('exact current source-bound Kafka confirmation receipt required')
 for name,want in [('topPASS',1),('subPASS',3),('skip',0),('fail',0),('packages',1),('newDatabaseFixtures',0),('actualKafkaACLTestsDispatched',1),('actualProcessImageTestsDispatched',0)]:
  if type(value[name]) is not int or value[name]!=want:raise ValueError('exact current Kafka confirmation counts required')
 expected={(package,top) for top in targets[package]}
 expected|={(package,top+'/'+child) for top,children in control['requiredSubtests'][package].items() for child in children}
 names=[(row['package'],row['name']) for row in value['namedCompletions']]
 if len(names)!=4 or set(names)!=expected:raise ValueError('current Kafka parent and every original/new child required')
 return value

def run_current_kafka_confirm_control(go,environment,command,parent,out):
 control=CURRENT_KAFKA_CONFIRM_CONTROL;targets=control['targets'];package=next(iter(targets));pattern='^('+'|'.join(re.escape(top) for top in targets[package])+')$'
 raw=command(control['phase'],[go,'test','-mod=readonly','-race','-p=1','-parallel=1','-count=1','-json','-vet=off','-timeout=180s','-run',pattern,'./'+package],control['phaseGuardSeconds'],environment)
 events=[json.loads(line) for line in raw.decode().splitlines() if line.strip()]
 result=parent.audit(events,0,targets)
 receipt=dict(result,source=FINAL_SOURCE,accepted=True,newDatabaseFixtures=0,scope=control,actualKafkaACLTestsDispatched=1,actualProcessImageTestsDispatched=0,samePackageSingleRaceInvocation=True,runtimeAccepted=False,wholeSourceAccepted=False)
 check_current_kafka_confirm_receipt(receipt)
 write(out/'current-kafka-confirm-names.json',receipt)
 write(out/'kafka-acl-historical-pass.json',check_kafka_acl_receipt(dict(KAFKA_ACL_PASSED_REUSE)))
 write(out/'process-image-historical-pass.json',check_process_image_prior(dict(PROCESS_IMAGE_PRIOR_PASS)))
 return receipt

def write_current_kafka_confirm_summary(out,receipt,cleanup_audit,launcher_reuse,redis_parser_reuse):
 check_current_kafka_confirm_receipt(receipt)
 write(out/'summary.json',{'accepted':True,'source':FINAL_SOURCE,'actualCurrentMainBuilds':14,'auxiliaryBuilds':2,'freshGraphScenarios':4,'pendingOriginalTopPASS':2,'pendingOriginalSubPASS':3,'cleanupTopPASS':0,'cleanupSubPASS':0,'cleanupPassedReuse':CLEANUP_PASSED_REUSE,'launcherTopPASS':0,'launcherSubPASS':0,'launcherPassedReuse':LAUNCHER_PASSED_REUSE,'actualLauncherTestsDispatched':0,'targetedTopPASS':2,'targetedSubPASS':3,'kafkaACLTopPASS':1,'kafkaACLSubPASS':3,'actualKafkaACLTestsDispatched':1,'processImageTopPASS':0,'processImageSubPASS':0,'actualProcessImageTestsDispatched':0,'processImageHistoricalPass':PROCESS_IMAGE_PRIOR_PASS,'currentKafkaConfirmScope':receipt,'kafkaACLPassedReuse':None,'kafkaACLHistoricalPass':KAFKA_ACL_PASSED_REUSE,'redisParserTopPASS':0,'redisParserSubPASS':0,'redisParserHistoricalTopPASS':1,'redisParserHistoricalSubPASS':3,'actualRedisParserTestsDispatched':0,'redisParserPassedReuse':redis_parser_reuse,'cleanupReuse':dict(CLEANUP_REUSE,topPASS=0,subPASS=1),'cleanupRecentReuse':CLEANUP_RECENT_REUSE,'historicalCleanupSourceFactsOnly':True,'currentWholeOwnedcontrolPackagePASS':False,'originalObligation':{'topCount':5,'subCount':12,'crossSource':True},'originalNewSafetyContract':{'topCount':2,'subCount':4,'crossSource':True},'originalCompleteObligation':{'topCount':7,'subCount':16,'crossSource':True},'currentFreshGo':{'topPASS':3,'subPASS':6,'includesOriginalGraph':True},'addedKafkaConfirmationChildPASS':1,'old67or21TestsRerun':0,'reuseOldBinary':False,'deployment':False,'resourceCleanup':'must independently pass always collector','privateLogsUploaded':False})

GATEWAY_START_LOG_POLICY = {'version': 'owned-gateway-start-log-projection-v2', 'logLimitBytes': 8388608, 'lineLimit': 16384, 'lineBytesLimit': 4096, 'framesLimit': 8, 'directoryEntriesLimit': 64, 'onlyRole': 'msggateway', 'onlyCachedWaitExitCode': 2, 'rawTextExported': False, 'acceptanceEvidence': False, 'panicPrefixClassesLimit': 8, 'wrapperLimit': 8}
KAFKA_CONFIRM_PASSED_REUSE = {'schema': 1, 'kind': 'root-accepted-current-whole-source-kafka-confirmation-reuse-v1', 'run': 42183524424, 'job': 127778379574, 'attempt': 1, 'head': '62175ab20f5f87a66c124517fc8a260ba1cddea5', 'source': {'members': 1576, 'digest': '7ac7af67952b4245dd5fc454494d7760f72b776bec4fe2a830fe4ea3c1496961'}, 'rootReviewSHA256': '640cd61969d65965a235d11880dc0a2a8828bf8a76424049728e5e586da38e2b', 'artifact': {'id': 13487280335, 'bytes': 50105, 'sha256': '6bd18d0e03313cde74a56754bb0d66445a809f0417a8ee19f57938307c3de6ab', 'exactAPIDigest': True, 'files': 65, 'binariesDownloaded': False}, 'originalReceipt': {'topPASS': 1, 'subPASS': 3, 'skip': 0, 'fail': 0, 'packages': 1, 'namedCompletions': [{'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny/create-reply-error-and-shape-cannot-be-a-success'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny/describe-reply-error-and-shape-cannot-replace-exact-deny'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny/new-deny-visibility-is-bounded-without-grace-for-existing-denies'}], 'source': {'members': 1576, 'digest': '7ac7af67952b4245dd5fc454494d7760f72b776bec4fe2a830fe4ea3c1496961'}, 'accepted': True, 'newDatabaseFixtures': 0, 'scope': {'phase': 'current-kafka-confirm', 'targets': {'internal/namespaceimport/ownedcontrol': ['TestOwnedKafkaExclusionRequiresEveryWildcardDeny']}, 'requiredSubtests': {'internal/namespaceimport/ownedcontrol': {'TestOwnedKafkaExclusionRequiresEveryWildcardDeny': ['create-reply-error-and-shape-cannot-be-a-success', 'describe-reply-error-and-shape-cannot-replace-exact-deny', 'new-deny-visibility-is-bounded-without-grace-for-existing-denies']}}, 'goTimeoutSeconds': 180, 'phaseGuardSeconds': 240, 'topPASS': 1, 'subPASS': 3, 'samePackageSingleRaceInvocation': True, 'newDatabaseFixtures': 0}, 'actualKafkaACLTestsDispatched': 1, 'actualProcessImageTestsDispatched': 0, 'samePackageSingleRaceInvocation': True, 'runtimeAccepted': False, 'wholeSourceAccepted': False}, 'originalEvents': [{'Time': '2026-11-06T07:52:50.182787218Z', 'Action': 'start', 'Package': 'github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol'}, {'Time': '2026-11-06T07:52:50.201536753Z', 'Action': 'run', 'Package': 'github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol', 'Test': 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny'}, {'Time': '2026-11-06T07:52:50.201623554Z', 'Action': 'output', 'Package': 'github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol', 'Test': 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny'}, {'Time': '2026-11-06T07:52:50.20166067Z', 'Action': 'run', 'Package': 'github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol', 'Test': 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny/create-reply-error-and-shape-cannot-be-a-success'}, {'Time': '2026-11-06T07:52:50.201664005Z', 'Action': 'output', 'Package': 'github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol', 'Test': 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny/create-reply-error-and-shape-cannot-be-a-success'}, {'Time': '2026-11-06T07:52:50.201716815Z', 'Action': 'output', 'Package': 'github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol', 'Test': 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny/create-reply-error-and-shape-cannot-be-a-success'}, {'Time': '2026-11-06T07:52:50.201724837Z', 'Action': 'pass', 'Package': 'github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol', 'Test': 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny/create-reply-error-and-shape-cannot-be-a-success', 'Elapsed': 0}, {'Time': '2026-11-06T07:52:50.201770295Z', 'Action': 'run', 'Package': 'github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol', 'Test': 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny/describe-reply-error-and-shape-cannot-replace-exact-deny'}, {'Time': '2026-11-06T07:52:50.201776314Z', 'Action': 'output', 'Package': 'github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol', 'Test': 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny/describe-reply-error-and-shape-cannot-replace-exact-deny'}, {'Time': '2026-11-06T07:52:50.201831338Z', 'Action': 'output', 'Package': 'github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol', 'Test': 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny/describe-reply-error-and-shape-cannot-replace-exact-deny'}, {'Time': '2026-11-06T07:52:50.201840441Z', 'Action': 'pass', 'Package': 'github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol', 'Test': 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny/describe-reply-error-and-shape-cannot-replace-exact-deny', 'Elapsed': 0}, {'Time': '2026-11-06T07:52:50.201870386Z', 'Action': 'run', 'Package': 'github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol', 'Test': 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny/new-deny-visibility-is-bounded-without-grace-for-existing-denies'}, {'Time': '2026-11-06T07:52:50.201875354Z', 'Action': 'output', 'Package': 'github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol', 'Test': 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny/new-deny-visibility-is-bounded-without-grace-for-existing-denies'}, {'Time': '2026-11-06T07:52:50.42300866Z', 'Action': 'output', 'Package': 'github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol', 'Test': 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny/new-deny-visibility-is-bounded-without-grace-for-existing-denies'}, {'Time': '2026-11-06T07:52:50.423076442Z', 'Action': 'pass', 'Package': 'github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol', 'Test': 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny/new-deny-visibility-is-bounded-without-grace-for-existing-denies', 'Elapsed': 0.22}, {'Time': '2026-11-06T07:52:50.423089582Z', 'Action': 'output', 'Package': 'github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol', 'Test': 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny'}, {'Time': '2026-11-06T07:52:50.423094049Z', 'Action': 'pass', 'Package': 'github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol', 'Test': 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny', 'Elapsed': 0.22}, {'Time': '2026-11-06T07:52:50.423156854Z', 'Action': 'output', 'Package': 'github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol'}, {'Time': '2026-11-06T07:52:51.426959618Z', 'Action': 'output', 'Package': 'github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol'}, {'Time': '2026-11-06T07:52:51.427018557Z', 'Action': 'pass', 'Package': 'github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol', 'Elapsed': 1.244}], 'originalPhase': {'phase': 'current-kafka-confirm', 'exitCode': 0, 'resource': {'status': 'passed', 'exit_code': 0}}, 'originalSourcePost': {'accepted': True, 'source': {'members': 1576, 'digest': '7ac7af67952b4245dd5fc454494d7760f72b776bec4fe2a830fe4ea3c1496961'}}, 'originalEvidencePins': {'current-kafka-confirm-names.json': {'bytes': 1568, 'sha256': 'e79ed22518ea4d7702e0837e5bcb6ed10e4529ddbd445d2af08c024885916d83'}, 'current-kafka-confirm-events.json': {'bytes': 4639, 'sha256': 'f269359a79caee47ede0a51bbc997713bb089602d40d2de7cb59c2a6fea1e2d9'}, 'phase-0004.json': {'bytes': 2522, 'sha256': '4b48809a66cefd4d605933ba8598a04a568dc7947b27d7ace5031ea19da7de14'}, 'source-post.json': {'bytes': 120, 'sha256': '91f451c87df02cfd2455a44ed47f54d4dc5e7240db1dff95ce75ada2fae43626'}, 'tools-before.json': {'bytes': 1277, 'sha256': '162cbbdb16858edd435d19cdc43701020164c9248ca316909caddff7d875ee5f'}, 'tools-after.json': {'bytes': 1277, 'sha256': '162cbbdb16858edd435d19cdc43701020164c9248ca316909caddff7d875ee5f'}}, 'goTool': {'bytes': 17164055, 'sha256': '548d3c32f83fa81152f543eb1526be7884cfe0526eb5e9ce29d3eca14786e7e0', 'mode': 493}, 'sourceToolsPostExact': True, 'originalActualKafkaTestsDispatched': 1, 'actualCurrentKafkaTestsDispatched': 0, 'currentFreshPASS': False, 'currentWholeOwnedcontrolPackagePASS': False, 'runtimeAccepted': False}

def check_kafka_confirm_reuse_receipt(value):
 if json.dumps(value,sort_keys=True,separators=(',',':'))!=json.dumps(KAFKA_CONFIRM_PASSED_REUSE,sort_keys=True,separators=(',',':')) or value['source']!=FINAL_SOURCE:raise ValueError('exact Root accepted current whole-source Kafka confirmation reuse required')
 original=check_current_kafka_confirm_receipt(value['originalReceipt'])
 events=value['originalEvents'];package=next(iter(CURRENT_KAFKA_CONFIRM_CONTROL['targets']));prefix='github.com/openimsdk/open-im-server/v3/'
 expected={(row['package'],row['name']) for row in original['namedCompletions']}
 if not isinstance(events,list) or not events or any(set(row)-{'Time','Action','Package','Test','Elapsed'} or row.get('Package')!=prefix+package or row.get('Action') in ('fail','skip') for row in events):raise ValueError('original finite current-source Kafka events required')
 for action in ('run','pass'):
  actual=[(package,row['Test']) for row in events if row.get('Test') and row.get('Action')==action]
  if len(actual)!=4 or set(actual)!=expected:raise ValueError('all four original Kafka run/pass pairs required')
 if len([row for row in events if row.get('Action')=='pass' and not row.get('Test')])!=1:raise ValueError('original Kafka package completion required')
 phase=value['originalPhase'];resource=phase.get('resource',{})
 if phase.get('phase')!=CURRENT_KAFKA_CONFIRM_CONTROL['phase'] or type(phase.get('exitCode')) is not int or phase['exitCode']!=0 or resource.get('status')!='passed' or type(resource.get('exit_code')) is not int or resource['exit_code']!=0:raise ValueError('original successful Kafka command required')
 if value['originalSourcePost']!={'accepted':True,'source':FINAL_SOURCE} or value['originalActualKafkaTestsDispatched']!=1 or value['actualCurrentKafkaTestsDispatched']!=0 or value['currentFreshPASS'] is not False or value['currentWholeOwnedcontrolPackagePASS'] is not False or value['runtimeAccepted'] is not False:raise ValueError('original invocation cannot become current dispatch or whole package acceptance')
 return value

def check_kafka_confirm_reuse_plan(c):
 if c.get('currentReleaseControl') is not None or c.get('currentManagedShapeControl') is not None:raise ValueError('same-source Kafka reuse cannot alias new controls')
 if c.get('gatewayStartLogProjectionPolicy')!=GATEWAY_START_LOG_POLICY:raise ValueError('exact finite gateway log projection policy required')
 if c.get('kafkaACLTestsToRun')!=[] or c.get('processImageTestsToRun')!=[] or c.get('kafkaACLPassedReuse') is not None or c.get('currentImageKafkaControl') is not None or c.get('currentKafkaConfirmControl') is not None:raise ValueError('current whole-source Kafka reuse must dispatch no old or current Kafka/image top')
 check_kafka_confirm_reuse_receipt(c.get('kafkaConfirmPassedReuse'));check_process_image_prior(c.get('processImageHistoricalPass'))
 return KAFKA_CONFIRM_PASSED_REUSE

def check_kafka_confirm_reuse(value,bodies,recipe,current_go=None):
 receipt=check_kafka_confirm_reuse_receipt(value)
 if recipe.digest(recipe.rows(bodies))!=receipt['source']['digest']:raise ValueError('entire current Kafka source must equal original accepted source')
 if current_go is not None:
  actual={key:current_go.get(key) for key in ('bytes','sha256','mode')}
  if json.dumps(actual,sort_keys=True)!=json.dumps(receipt['goTool'],sort_keys=True):raise ValueError('original current-source Kafka Go executable differs')
 return receipt

def load_kafka_confirm_reuse(out,bodies,recipe,current_go):
 receipt=check_kafka_confirm_reuse(dict(KAFKA_CONFIRM_PASSED_REUSE),bodies,recipe,current_go)
 write(out/'kafka-confirm-passed-reuse.json',receipt)
 return receipt

def write_kafka_confirm_reuse_summary(out,receipt,cleanup_audit,launcher_reuse,redis_parser_reuse):
 check_kafka_confirm_reuse_receipt(receipt)
 write(out/'summary.json',{'accepted':True,'source':FINAL_SOURCE,'actualCurrentMainBuilds':14,'auxiliaryBuilds':2,'freshGraphScenarios':4,'pendingOriginalTopPASS':2,'pendingOriginalSubPASS':3,'cleanupTopPASS':0,'cleanupSubPASS':0,'cleanupPassedReuse':CLEANUP_PASSED_REUSE,'launcherTopPASS':0,'launcherSubPASS':0,'launcherPassedReuse':LAUNCHER_PASSED_REUSE,'actualLauncherTestsDispatched':0,'targetedTopPASS':2,'targetedSubPASS':3,'kafkaACLTopPASS':0,'kafkaACLSubPASS':0,'originalSameSourceKafkaTopPASS':1,'originalSameSourceKafkaSubPASS':3,'actualKafkaACLTestsDispatched':0,'kafkaConfirmationPassedReuse':receipt,'processImageTopPASS':0,'processImageSubPASS':0,'actualProcessImageTestsDispatched':0,'processImageHistoricalPass':PROCESS_IMAGE_PRIOR_PASS,'kafkaACLPassedReuse':None,'kafkaACLHistoricalPass':KAFKA_ACL_PASSED_REUSE,'redisParserTopPASS':0,'redisParserSubPASS':0,'redisParserHistoricalTopPASS':1,'redisParserHistoricalSubPASS':3,'actualRedisParserTestsDispatched':0,'redisParserPassedReuse':redis_parser_reuse,'cleanupReuse':dict(CLEANUP_REUSE,topPASS=0,subPASS=1),'cleanupRecentReuse':CLEANUP_RECENT_REUSE,'historicalCleanupSourceFactsOnly':True,'currentWholeOwnedcontrolPackagePASS':False,'originalObligation':{'topCount':5,'subCount':12,'crossSource':True},'originalNewSafetyContract':{'topCount':2,'subCount':4,'crossSource':True},'originalCompleteObligation':{'topCount':7,'subCount':16,'crossSource':True},'currentFreshGo':{'topPASS':2,'subPASS':3,'includesOriginalGraph':True},'old67or21TestsRerun':0,'reuseOldBinary':False,'deployment':False,'resourceCleanup':'must independently pass always collector','privateLogsUploaded':False})

CURRENT_RELEASE_CONTROL = {'phase': 'current-release-controls', 'targets': {'internal/namespaceimport/ownedcontrol': ['TestNamespaceRuntimeMissingReleaseWaitsWithoutWeakeningPrivateEvidence', 'TestNamespaceRuntimeRegistrationIsPrivateOnceAndNeverAReadyPermit', 'TestOwnedServerRuntimeEvidenceDoesNotInventLegacyEpoch', 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny']}, 'requiredSubtests': {'internal/namespaceimport/ownedcontrol': {'TestNamespaceRuntimeMissingReleaseWaitsWithoutWeakeningPrivateEvidence': ['missing-release-waits-and-cancels-without-ready', 'malformed-release', 'empty-release', 'public-mode-release', 'symlink-release', 'hardlink-release', 'directory-release', 'absent-parent-is-not-release-pending', 'aliased-parent-is-not-release-pending', 'missing-announcement-cannot-be-ready'], 'TestNamespaceRuntimeRegistrationIsPrivateOnceAndNeverAReadyPermit': ['actual-exclusive-registration-still-not-permission', 'second-registration-cannot-change-original-resource-identity', 'unsigned-or-foreign-release-remains-closed', 'retirement-is-sticky'], 'TestOwnedServerRuntimeEvidenceDoesNotInventLegacyEpoch': ['legacy_actual_identity_round_trip', 'legacy_target_epoch_rejected', 'required_must_carry_actual_runtime_identity', 'unknown_role_cannot_self_enroll', 'drain_exact_nonce_and_complete_ordered_join'], 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny': ['create-reply-error-and-shape-cannot-be-a-success', 'describe-reply-error-and-shape-cannot-replace-exact-deny', 'new-deny-visibility-is-bounded-without-grace-for-existing-denies']}}, 'goTimeoutSeconds': 180, 'phaseGuardSeconds': 240, 'topPASS': 4, 'subPASS': 22, 'samePackageSingleRaceInvocation': True, 'newDatabaseFixtures': 0, 'releaseIndividualTopPASS': 3, 'releaseIndividualSubPASS': 19, 'kafkaIndividualTopPASS': 1, 'kafkaIndividualSubPASS': 3, 'wholeOwnedcontrolPackagePASS': False}
KAFKA_CONFIRM_PRIOR_PASS = KAFKA_CONFIRM_PASSED_REUSE

def check_kafka_confirm_prior_pass(value):
 if json.dumps(value,sort_keys=True,separators=(',',':'))!=json.dumps(KAFKA_CONFIRM_PRIOR_PASS,sort_keys=True,separators=(',',':')) or value['source']==FINAL_SOURCE:raise ValueError('exact prior 7ac Kafka single-item fact only; not current source reuse')
 original=value['originalReceipt']
 if original['source']!=value['source'] or original['topPASS']!=1 or original['subPASS']!=3 or value['originalActualKafkaTestsDispatched']!=1 or value['currentFreshPASS'] is not False or value['currentWholeOwnedcontrolPackagePASS'] is not False:raise ValueError('original Kafka invocation and individual counts must remain historical')
 return value

def check_current_release_plan(c):
 if c.get('currentManagedShapeControl') is not None:raise ValueError('earlier release control cannot alias new managed scope')
 control=CURRENT_RELEASE_CONTROL;package=next(iter(control['targets']));tops=control['targets'][package]
 if c.get('currentReleaseControl')!=control or c.get('currentReleaseTestsToRun')!=tops[:-1] or c.get('kafkaACLTestsToRun')!=tops[-1:] or c.get('processImageTestsToRun')!=[]:raise ValueError('exact fresh release3top19sub plus Kafka1top3sub in one race invocation required')
 if any(c.get(key) is not None for key in ('kafkaACLPassedReuse','currentImageKafkaControl','currentKafkaConfirmControl','kafkaConfirmPassedReuse')):raise ValueError('fresh release control cannot alias any older fresh or reuse branch')
 if c.get('gatewayStartLogProjectionPolicy')!=GATEWAY_START_LOG_POLICY:raise ValueError('exact existing bounded diagnostic policy required')
 check_kafka_confirm_prior_pass(c.get('kafkaConfirmHistoricalPass'));check_kafka_acl_receipt(c.get('kafkaACLHistoricalPass'));check_process_image_prior(c.get('processImageHistoricalPass'))
 return control

def release_kafka_individual_fact(value):
 package=next(iter(CURRENT_KAFKA_CONFIRM_CONTROL['targets']));top=CURRENT_KAFKA_CONFIRM_CONTROL['targets'][package][0]
 names=sorted([dict(row) for row in value['namedCompletions'] if row['package']==package and (row['name']==top or row['name'].startswith(top+'/'))],key=lambda row:row['name'])
 return {'version':'single-kafka-item-within-release-race-v1','source':FINAL_SOURCE,'topPASS':1,'subPASS':3,'namedCompletions':names,'withinPhase':CURRENT_RELEASE_CONTROL['phase'],'sharedRaceInvocation':1,'separateKafkaInvocations':0,'currentWholeOwnedcontrolPackagePASS':False,'runtimeAccepted':False}

def check_current_release_receipt(value):
 control=CURRENT_RELEASE_CONTROL;targets=control['targets'];package=next(iter(targets))
 keys={'topPASS','subPASS','skip','fail','packages','namedCompletions','source','accepted','newDatabaseFixtures','scope','actualKafkaACLTestsDispatched','actualProcessImageTestsDispatched','actualReleaseTopsDispatched','actualOwnedcontrolRaceInvocations','samePackageSingleRaceInvocation','individualKafkaFact','runtimeAccepted','wholeSourceAccepted'}
 if not isinstance(value,dict) or set(value)!=keys or value['source']!=FINAL_SOURCE or value['scope']!=control or value['accepted'] is not True or value['samePackageSingleRaceInvocation'] is not True or value['runtimeAccepted'] is not False or value['wholeSourceAccepted'] is not False:raise ValueError('exact current source-bound partial-package release receipt required')
 for name,want in [('topPASS',4),('subPASS',22),('skip',0),('fail',0),('packages',1),('newDatabaseFixtures',0),('actualKafkaACLTestsDispatched',1),('actualProcessImageTestsDispatched',0),('actualReleaseTopsDispatched',3),('actualOwnedcontrolRaceInvocations',1)]:
  if type(value[name]) is not int or value[name]!=want:raise ValueError('exact fresh release and Kafka individual counts required')
 expected={(package,top) for top in targets[package]}
 expected|={(package,top+'/'+child) for top,children in control['requiredSubtests'][package].items() for child in children}
 actual=[(row['package'],row['name']) for row in value['namedCompletions']]
 if len(actual)!=26 or set(actual)!=expected or value['individualKafkaFact']!=release_kafka_individual_fact(value):raise ValueError('all four original tops and twenty-two exact children required')
 return value

def run_current_release_control(go,environment,command,parent,out):
 control=CURRENT_RELEASE_CONTROL;targets=control['targets'];package=next(iter(targets));pattern='^('+'|'.join(re.escape(top) for top in targets[package])+')$'
 raw=command(control['phase'],[go,'test','-mod=readonly','-race','-p=1','-parallel=1','-count=1','-json','-vet=off','-timeout=180s','-run',pattern,'./'+package],control['phaseGuardSeconds'],environment)
 events=[json.loads(line) for line in raw.decode().splitlines() if line.strip()];result=parent.audit(events,0,targets)
 value=dict(result,source=FINAL_SOURCE,accepted=True,newDatabaseFixtures=0,scope=control,actualKafkaACLTestsDispatched=1,actualProcessImageTestsDispatched=0,actualReleaseTopsDispatched=3,actualOwnedcontrolRaceInvocations=1,samePackageSingleRaceInvocation=True,runtimeAccepted=False,wholeSourceAccepted=False)
 value['individualKafkaFact']=release_kafka_individual_fact(value);check_current_release_receipt(value)
 write(out/'current-release-controls-names.json',value);write(out/'current-release-kafka-individual.json',value['individualKafkaFact'])
 write(out/'kafka-confirm-historical-pass.json',check_kafka_confirm_prior_pass(dict(KAFKA_CONFIRM_PRIOR_PASS)))
 write(out/'kafka-acl-historical-pass.json',check_kafka_acl_receipt(dict(KAFKA_ACL_PASSED_REUSE)));write(out/'process-image-historical-pass.json',check_process_image_prior(dict(PROCESS_IMAGE_PRIOR_PASS)))
 return value

def write_current_release_summary(out,receipt,cleanup_audit,launcher_reuse,redis_parser_reuse):
 check_current_release_receipt(receipt)
 write(out/'summary.json',{'accepted':True,'source':FINAL_SOURCE,'actualCurrentMainBuilds':14,'auxiliaryBuilds':2,'freshGraphScenarios':4,'pendingOriginalTopPASS':2,'pendingOriginalSubPASS':3,'targetedTopPASS':2,'targetedSubPASS':3,'releaseControlTopPASS':4,'releaseControlSubPASS':22,'actualOwnedcontrolRaceInvocations':1,'actualReleaseTopsDispatched':3,'kafkaACLTopPASS':1,'kafkaACLSubPASS':3,'actualKafkaACLTestsDispatched':1,'individualKafkaFact':receipt['individualKafkaFact'],'currentReleaseScope':receipt,'currentWholeOwnedcontrolPackagePASS':False,'processImageTopPASS':0,'processImageSubPASS':0,'actualProcessImageTestsDispatched':0,'processImageHistoricalPass':PROCESS_IMAGE_PRIOR_PASS,'kafkaConfirmHistoricalPass':KAFKA_CONFIRM_PRIOR_PASS,'kafkaConfirmPassedReuse':None,'kafkaACLPassedReuse':None,'kafkaACLHistoricalPass':KAFKA_ACL_PASSED_REUSE,'cleanupPassedReuse':CLEANUP_PASSED_REUSE,'cleanupTopPASS':0,'cleanupSubPASS':0,'cleanupRecentReuse':CLEANUP_RECENT_REUSE,'cleanupReuse':dict(CLEANUP_REUSE,topPASS=0,subPASS=1),'launcherPassedReuse':launcher_reuse,'actualLauncherTestsDispatched':0,'redisParserPassedReuse':redis_parser_reuse,'actualRedisParserTestsDispatched':0,'affectedPriorControlsRedispatched':CURRENT_RELEASE_CONTROL['targets'][next(iter(CURRENT_RELEASE_CONTROL['targets']))][1:3],'unaffectedHistoricalBatchesRedispatched':False,'historicalCleanupSourceFactsOnly':True,'originalObligation':{'topCount':5,'subCount':12,'crossSource':True},'originalNewSafetyContract':{'topCount':2,'subCount':4,'crossSource':True},'originalCompleteObligation':{'topCount':7,'subCount':16,'crossSource':True},'currentFreshGo':{'topPASS':6,'subPASS':25,'includesOriginalGraph':True},'reuseOldBinary':False,'deployment':False,'resourceCleanup':'must independently pass always collector','privateLogsUploaded':False})

CURRENT_MANAGED_SHAPE_CONTROL = {'phase': 'current-managed-shape-controls', 'targets': {'internal/namespaceimport/ownedcontrol': ['TestOwnedKafkaExclusionRequiresEveryWildcardDeny'], 'internal/msgtransfer': ['TestManagedOrdinaryProducerMatchesNamespaceLaunchCanonicalShape']}, 'requiredSubtests': {'internal/namespaceimport/ownedcontrol': {'TestOwnedKafkaExclusionRequiresEveryWildcardDeny': ['create-reply-error-and-shape-cannot-be-a-success', 'describe-reply-error-and-shape-cannot-replace-exact-deny', 'new-deny-visibility-is-bounded-without-grace-for-existing-denies']}, 'internal/msgtransfer': {'TestManagedOrdinaryProducerMatchesNamespaceLaunchCanonicalShape': ['producer-and-child-loader-roundtrip', 'alphabetic-map-reorder', 'unknown-field', 'missing-field', 'duplicate-field', 'wrong-value-type', 'null-string', 'wrong-pin', 'public-file', 'symlink-file', 'hardlink-file', 'trailing-json', 'missing-file']}}, 'goTimeoutSeconds': 180, 'phaseGuardSeconds': 240, 'topPASS': 2, 'subPASS': 16, 'packages': 2, 'goCommandInvocations': 1, 'packageExecutions': 2, 'samePackageInvocation': False, 'newDatabaseFixtures': 0, 'wholePackagePASS': False}
RELEASE_CONTROLS_PRIOR_PASS = {'schema': 1, 'kind': 'root-accepted-f3fb-release-controls-historical-fact-v1', 'run': 42201608477, 'job': 127837988902, 'attempt': 1, 'head': 'f3fb36758f6af1fd9e29bd4559699a7d56fdfe24', 'source': {'members': 1576, 'digest': 'ffd081874f7c2eaf0c0ec6e3e24e52796662dd8fa3d3bf16421f98bfd1ac5c82'}, 'rootReviewSHA256': '58aebceb715ddd8f1e78e6c2a508ac3c7ad663c60f74d43950e369c1fc68b745', 'originalReceipt': {'topPASS': 4, 'subPASS': 22, 'skip': 0, 'fail': 0, 'packages': 1, 'namedCompletions': [{'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestNamespaceRuntimeMissingReleaseWaitsWithoutWeakeningPrivateEvidence'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestNamespaceRuntimeMissingReleaseWaitsWithoutWeakeningPrivateEvidence/absent-parent-is-not-release-pending'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestNamespaceRuntimeMissingReleaseWaitsWithoutWeakeningPrivateEvidence/aliased-parent-is-not-release-pending'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestNamespaceRuntimeMissingReleaseWaitsWithoutWeakeningPrivateEvidence/directory-release'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestNamespaceRuntimeMissingReleaseWaitsWithoutWeakeningPrivateEvidence/empty-release'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestNamespaceRuntimeMissingReleaseWaitsWithoutWeakeningPrivateEvidence/hardlink-release'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestNamespaceRuntimeMissingReleaseWaitsWithoutWeakeningPrivateEvidence/malformed-release'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestNamespaceRuntimeMissingReleaseWaitsWithoutWeakeningPrivateEvidence/missing-announcement-cannot-be-ready'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestNamespaceRuntimeMissingReleaseWaitsWithoutWeakeningPrivateEvidence/missing-release-waits-and-cancels-without-ready'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestNamespaceRuntimeMissingReleaseWaitsWithoutWeakeningPrivateEvidence/public-mode-release'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestNamespaceRuntimeMissingReleaseWaitsWithoutWeakeningPrivateEvidence/symlink-release'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestNamespaceRuntimeRegistrationIsPrivateOnceAndNeverAReadyPermit'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestNamespaceRuntimeRegistrationIsPrivateOnceAndNeverAReadyPermit/actual-exclusive-registration-still-not-permission'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestNamespaceRuntimeRegistrationIsPrivateOnceAndNeverAReadyPermit/retirement-is-sticky'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestNamespaceRuntimeRegistrationIsPrivateOnceAndNeverAReadyPermit/second-registration-cannot-change-original-resource-identity'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestNamespaceRuntimeRegistrationIsPrivateOnceAndNeverAReadyPermit/unsigned-or-foreign-release-remains-closed'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny/create-reply-error-and-shape-cannot-be-a-success'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny/describe-reply-error-and-shape-cannot-replace-exact-deny'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny/new-deny-visibility-is-bounded-without-grace-for-existing-denies'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedServerRuntimeEvidenceDoesNotInventLegacyEpoch'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedServerRuntimeEvidenceDoesNotInventLegacyEpoch/drain_exact_nonce_and_complete_ordered_join'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedServerRuntimeEvidenceDoesNotInventLegacyEpoch/legacy_actual_identity_round_trip'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedServerRuntimeEvidenceDoesNotInventLegacyEpoch/legacy_target_epoch_rejected'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedServerRuntimeEvidenceDoesNotInventLegacyEpoch/required_must_carry_actual_runtime_identity'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedServerRuntimeEvidenceDoesNotInventLegacyEpoch/unknown_role_cannot_self_enroll'}], 'source': {'members': 1576, 'digest': 'ffd081874f7c2eaf0c0ec6e3e24e52796662dd8fa3d3bf16421f98bfd1ac5c82'}, 'accepted': True, 'newDatabaseFixtures': 0, 'scope': {'phase': 'current-release-controls', 'targets': {'internal/namespaceimport/ownedcontrol': ['TestNamespaceRuntimeMissingReleaseWaitsWithoutWeakeningPrivateEvidence', 'TestNamespaceRuntimeRegistrationIsPrivateOnceAndNeverAReadyPermit', 'TestOwnedServerRuntimeEvidenceDoesNotInventLegacyEpoch', 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny']}, 'requiredSubtests': {'internal/namespaceimport/ownedcontrol': {'TestNamespaceRuntimeMissingReleaseWaitsWithoutWeakeningPrivateEvidence': ['missing-release-waits-and-cancels-without-ready', 'malformed-release', 'empty-release', 'public-mode-release', 'symlink-release', 'hardlink-release', 'directory-release', 'absent-parent-is-not-release-pending', 'aliased-parent-is-not-release-pending', 'missing-announcement-cannot-be-ready'], 'TestNamespaceRuntimeRegistrationIsPrivateOnceAndNeverAReadyPermit': ['actual-exclusive-registration-still-not-permission', 'second-registration-cannot-change-original-resource-identity', 'unsigned-or-foreign-release-remains-closed', 'retirement-is-sticky'], 'TestOwnedServerRuntimeEvidenceDoesNotInventLegacyEpoch': ['legacy_actual_identity_round_trip', 'legacy_target_epoch_rejected', 'required_must_carry_actual_runtime_identity', 'unknown_role_cannot_self_enroll', 'drain_exact_nonce_and_complete_ordered_join'], 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny': ['create-reply-error-and-shape-cannot-be-a-success', 'describe-reply-error-and-shape-cannot-replace-exact-deny', 'new-deny-visibility-is-bounded-without-grace-for-existing-denies']}}, 'goTimeoutSeconds': 180, 'phaseGuardSeconds': 240, 'topPASS': 4, 'subPASS': 22, 'samePackageSingleRaceInvocation': True, 'newDatabaseFixtures': 0, 'releaseIndividualTopPASS': 3, 'releaseIndividualSubPASS': 19, 'kafkaIndividualTopPASS': 1, 'kafkaIndividualSubPASS': 3, 'wholeOwnedcontrolPackagePASS': False}, 'actualKafkaACLTestsDispatched': 1, 'actualProcessImageTestsDispatched': 0, 'actualReleaseTopsDispatched': 3, 'actualOwnedcontrolRaceInvocations': 1, 'samePackageSingleRaceInvocation': True, 'runtimeAccepted': False, 'wholeSourceAccepted': False, 'individualKafkaFact': {'version': 'single-kafka-item-within-release-race-v1', 'source': {'members': 1576, 'digest': 'ffd081874f7c2eaf0c0ec6e3e24e52796662dd8fa3d3bf16421f98bfd1ac5c82'}, 'topPASS': 1, 'subPASS': 3, 'namedCompletions': [{'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny/create-reply-error-and-shape-cannot-be-a-success'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny/describe-reply-error-and-shape-cannot-replace-exact-deny'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny/new-deny-visibility-is-bounded-without-grace-for-existing-denies'}], 'withinPhase': 'current-release-controls', 'sharedRaceInvocation': 1, 'separateKafkaInvocations': 0, 'currentWholeOwnedcontrolPackagePASS': False, 'runtimeAccepted': False}}, 'originalPhase': {'phase': 'current-release-controls', 'exitCode': 0, 'resource': {'status': 'passed', 'exit_code': 0}}, 'originalSourcePost': {'accepted': True, 'source': {'members': 1576, 'digest': 'ffd081874f7c2eaf0c0ec6e3e24e52796662dd8fa3d3bf16421f98bfd1ac5c82'}}, 'originalEvidencePins': {'current-release-controls-names.json': {'bytes': 7258, 'sha256': '4082f91c2ab2cc6d59996a1799f6c1c7439f9fe91dc464b7a2f684608d31b2de'}, 'current-release-controls-events.json': {'bytes': 26976, 'sha256': '94ae54d8f2ffaf9f30f5b5079f646b60427a43ffe9b0f9154022bc006be9db7e'}, 'current-release-kafka-individual.json': {'bytes': 976, 'sha256': '7920ea2297210b6d49ae3dcb2f4213296e832f1a59fe5a81ac2679a74d1b2e2d'}, 'phase-0004.json': {'bytes': 2728, 'sha256': 'e26e0fc30624bdf57ce4d586f5b6bd14a90797f2f5aae04d8990f1a1e79b928b'}, 'source-post.json': {'bytes': 120, 'sha256': '0ead118cf69e2d5d1951b9f69c5bb581a8212075bde06eedb2b0397a10001340'}, 'tools-before.json': {'bytes': 1277, 'sha256': '162cbbdb16858edd435d19cdc43701020164c9248ca316909caddff7d875ee5f'}, 'tools-after.json': {'bytes': 1277, 'sha256': '162cbbdb16858edd435d19cdc43701020164c9248ca316909caddff7d875ee5f'}}, 'goTool': {'bytes': 17164055, 'sha256': '548d3c32f83fa81152f543eb1526be7884cfe0526eb5e9ce29d3eca14786e7e0', 'mode': 493}, 'originalGoCommandInvocations': 1, 'originalPackageExecutions': 1, 'actualCurrentCommandsDispatched': 0, 'currentSourcePASS': False, 'currentPackagePASS': False, 'runtimeAccepted': False}

def check_release_controls_prior_pass(value):
 if json.dumps(value,sort_keys=True,separators=(',',':'))!=json.dumps(RELEASE_CONTROLS_PRIOR_PASS,sort_keys=True,separators=(',',':')) or value['source']==FINAL_SOURCE:raise ValueError('exact f3fb release-control fact only; not current-source acceptance')
 original=value['originalReceipt']
 if original['source']!=value['source'] or (original['topPASS'],original['subPASS'],original['packages'])!=(4,22,1) or value['originalGoCommandInvocations']!=1 or value['originalPackageExecutions']!=1 or value['currentSourcePASS'] is not False or value['runtimeAccepted'] is not False:raise ValueError('original release counts and source remain historical')
 return value

def check_current_managed_shape_plan(c):
 control=CURRENT_MANAGED_SHAPE_CONTROL;owned='internal/namespaceimport/ownedcontrol';managed='internal/msgtransfer'
 if c.get('currentManagedShapeControl')!=control or c.get('currentManagedShapeTestsToRun')!=control['targets'][managed] or c.get('kafkaACLTestsToRun')!=control['targets'][owned] or c.get('processImageTestsToRun')!=[]:raise ValueError('exact two-package managed-shape/Kafka scope required')
 if any(c.get(key) is not None for key in ('currentReleaseControl','kafkaACLPassedReuse','currentImageKafkaControl','currentKafkaConfirmControl','kafkaConfirmPassedReuse')) or c.get('currentReleaseTestsToRun')!=[]:raise ValueError('two-package scope cannot alias any earlier current or reuse branch')
 if c.get('gatewayStartLogProjectionPolicy')!=GATEWAY_START_LOG_POLICY:raise ValueError('existing finite diagnostic policy differs')
 check_release_controls_prior_pass(c.get('releaseControlsHistoricalPass'));check_kafka_confirm_prior_pass(c.get('kafkaConfirmHistoricalPass'));check_kafka_acl_receipt(c.get('kafkaACLHistoricalPass'));check_process_image_prior(c.get('processImageHistoricalPass'))
 return control

def managed_shape_package_facts(value):
 control=CURRENT_MANAGED_SHAPE_CONTROL;facts={}
 for package,tops in control['targets'].items():
  names=sorted([dict(row) for row in value['namedCompletions'] if row['package']==package],key=lambda row:row['name'])
  facts[package]={'source':FINAL_SOURCE,'topPASS':len(tops),'subPASS':sum(len(control['requiredSubtests'][package][top]) for top in tops),'namedCompletions':names,'withinCommand':control['phase'],'packageExecutions':1,'wholePackagePASS':False,'runtimeAccepted':False}
 return facts

def managed_shape_kafka_fact(value):
 package='internal/namespaceimport/ownedcontrol';fact=dict(managed_shape_package_facts(value)[package])
 fact.update(version='single-kafka-item-within-two-package-command-v1',package=package,commandInvocations=1,commandPackages=2,separateKafkaCommandInvocations=0)
 return fact

def check_current_managed_shape_receipt(value):
 control=CURRENT_MANAGED_SHAPE_CONTROL
 keys={'topPASS','subPASS','skip','fail','packages','namedCompletions','source','accepted','newDatabaseFixtures','scope','actualGoCommandInvocations','actualPackageExecutions','actualKafkaACLTestsDispatched','actualManagedShapeTopsDispatched','actualProcessImageTestsDispatched','samePackageSingleRaceInvocation','packageFacts','individualKafkaFact','runtimeAccepted','wholeSourceAccepted'}
 if not isinstance(value,dict) or set(value)!=keys or value['source']!=FINAL_SOURCE or value['scope']!=control or value['accepted'] is not True or value['samePackageSingleRaceInvocation'] is not False or value['runtimeAccepted'] is not False or value['wholeSourceAccepted'] is not False:raise ValueError('exact current two-package partial-scope receipt required')
 for name,want in [('topPASS',2),('subPASS',16),('skip',0),('fail',0),('packages',2),('newDatabaseFixtures',0),('actualGoCommandInvocations',1),('actualPackageExecutions',2),('actualKafkaACLTestsDispatched',1),('actualManagedShapeTopsDispatched',1),('actualProcessImageTestsDispatched',0)]:
  if type(value[name]) is not int or value[name]!=want:raise ValueError('exact command/package/subset counts required')
 expected={(package,top) for package,tops in control['targets'].items() for top in tops}
 expected|={(package,top+'/'+child) for package,parents in control['requiredSubtests'].items() for top,children in parents.items() for child in children}
 actual=[(row['package'],row['name']) for row in value['namedCompletions']]
 if len(actual)!=18 or set(actual)!=expected or json.dumps(value['packageFacts'],sort_keys=True)!=json.dumps(managed_shape_package_facts(value),sort_keys=True) or json.dumps(value['individualKafkaFact'],sort_keys=True)!=json.dumps(managed_shape_kafka_fact(value),sort_keys=True):raise ValueError('two original package subsets and every exact child required')
 return value

def run_current_managed_shape_control(go,environment,command,parent,out):
 control=CURRENT_MANAGED_SHAPE_CONTROL;targets=control['targets'];tops=[top for names in targets.values() for top in names];pattern='^('+'|'.join(re.escape(top) for top in tops)+')$'
 raw=command(control['phase'],[go,'test','-mod=readonly','-race','-p=1','-parallel=1','-count=1','-json','-vet=off','-timeout=180s','-run',pattern]+['./'+package for package in targets],control['phaseGuardSeconds'],environment)
 events=[json.loads(line) for line in raw.decode().splitlines() if line.strip()];result=parent.audit(events,0,targets)
 value=dict(result,source=FINAL_SOURCE,accepted=True,newDatabaseFixtures=0,scope=control,actualGoCommandInvocations=1,actualPackageExecutions=2,actualKafkaACLTestsDispatched=1,actualManagedShapeTopsDispatched=1,actualProcessImageTestsDispatched=0,samePackageSingleRaceInvocation=False,runtimeAccepted=False,wholeSourceAccepted=False)
 value['packageFacts']=managed_shape_package_facts(value);value['individualKafkaFact']=managed_shape_kafka_fact(value);check_current_managed_shape_receipt(value)
 write(out/'current-managed-shape-controls-names.json',value);write(out/'current-managed-shape-kafka-individual.json',value['individualKafkaFact'])
 write(out/'release-controls-historical-pass.json',check_release_controls_prior_pass(dict(RELEASE_CONTROLS_PRIOR_PASS)));write(out/'kafka-confirm-historical-pass.json',check_kafka_confirm_prior_pass(dict(KAFKA_CONFIRM_PRIOR_PASS)))
 write(out/'kafka-acl-historical-pass.json',check_kafka_acl_receipt(dict(KAFKA_ACL_PASSED_REUSE)));write(out/'process-image-historical-pass.json',check_process_image_prior(dict(PROCESS_IMAGE_PRIOR_PASS)))
 return value

def write_current_managed_shape_summary(out,receipt,cleanup_audit,launcher_reuse,redis_parser_reuse):
 check_current_managed_shape_receipt(receipt)
 write(out/'summary.json',{'accepted':True,'source':FINAL_SOURCE,'actualCurrentMainBuilds':14,'auxiliaryBuilds':2,'freshGraphScenarios':4,'pendingOriginalTopPASS':2,'pendingOriginalSubPASS':3,'targetedTopPASS':2,'targetedSubPASS':3,'managedShapeControlTopPASS':2,'managedShapeControlSubPASS':16,'managedShapeCommandInvocations':1,'managedShapePackageExecutions':2,'samePackageSingleRaceInvocation':False,'packageFacts':receipt['packageFacts'],'individualKafkaFact':receipt['individualKafkaFact'],'actualKafkaACLTestsDispatched':1,'actualManagedShapeTopsDispatched':1,'currentManagedShapeScope':receipt,'currentWholeOwnedcontrolPackagePASS':False,'currentWholeMsgtransferPackagePASS':False,'actualProcessImageTestsDispatched':0,'processImageHistoricalPass':PROCESS_IMAGE_PRIOR_PASS,'releaseControlsHistoricalPass':RELEASE_CONTROLS_PRIOR_PASS,'kafkaConfirmHistoricalPass':KAFKA_CONFIRM_PRIOR_PASS,'kafkaConfirmPassedReuse':None,'kafkaACLPassedReuse':None,'kafkaACLHistoricalPass':KAFKA_ACL_PASSED_REUSE,'cleanupPassedReuse':CLEANUP_PASSED_REUSE,'cleanupRecentReuse':CLEANUP_RECENT_REUSE,'cleanupReuse':dict(CLEANUP_REUSE,topPASS=0,subPASS=1),'launcherPassedReuse':launcher_reuse,'actualLauncherTestsDispatched':0,'redisParserPassedReuse':redis_parser_reuse,'actualRedisParserTestsDispatched':0,'unaffectedHistoricalBatchesRedispatched':False,'historicalCleanupSourceFactsOnly':True,'originalObligation':{'topCount':5,'subCount':12,'crossSource':True},'originalNewSafetyContract':{'topCount':2,'subCount':4,'crossSource':True},'originalCompleteObligation':{'topCount':7,'subCount':16,'crossSource':True},'currentFreshGo':{'topPASS':4,'subPASS':19,'includesOriginalGraph':True},'reuseOldBinary':False,'deployment':False,'resourceCleanup':'must independently pass always collector','privateLogsUploaded':False})

CURRENT_PRODUCER_CONTROL = {'phase': 'current-managed-producer-control', 'targets': {'internal/msgtransfer': ['TestManagedOrdinaryProducerMatchesNamespaceLaunchCanonicalShape']}, 'requiredSubtests': {'internal/msgtransfer': {'TestManagedOrdinaryProducerMatchesNamespaceLaunchCanonicalShape': ['producer-and-child-loader-roundtrip', 'alphabetic-map-reorder', 'unknown-field', 'missing-field', 'duplicate-field', 'wrong-value-type', 'null-string', 'wrong-pin', 'public-file', 'symlink-file', 'hardlink-file', 'trailing-json', 'missing-file']}}, 'goTimeoutSeconds': 180, 'phaseGuardSeconds': 600, 'topPASS': 1, 'subPASS': 13, 'packages': 1, 'goCommandInvocations': 1, 'packageExecutions': 1, 'newDatabaseFixtures': 0, 'wholePackagePASS': False}
COMPLETED_KAFKA_PACKAGE_BINDING = {'schema': 1, 'kind': 'root-accepted-same-source-completed-kafka-package-in-timeout-command-v1', 'run': 42206676039, 'job': 127854974346, 'attempt': 1, 'head': 'cdd8f236614ba32f575367748b52137abf9bc63a', 'source': {'members': 1576, 'digest': '3498242c509e6e7f75ec5e00c1392f772ba0feca5596d60a35c7320f04744fea'}, 'rootReviewSHA256': '9b556b4d4ff96a6c52047803e9b713b7fb77548930a3ca0a4457f8a83b58edf0', 'artifactID': 13496927972, 'artifactSHA256': '7e6fa52a27545c51ec846235a498a9d3d1168b5aa786488cbfe85632805b70e6', 'eventsSHA256': '2c6b853aa4b39d1cabeb897e2b3f996deb57ab449621a8e1927dfad8574db06a', 'phaseSHA256': '9df468c689f414756aa6f9a5751d9583b941f8184795762cbc1d39126b6dc734', 'sourcePostSHA256': 'd3bbe21afff29a4e07926c3b53ce225790840422fd1fe7c7bde17e9b631a63fc', 'toolsSHA256': '162cbbdb16858edd435d19cdc43701020164c9248ca316909caddff7d875ee5f', 'originalPackageReceipt': {'source': {'members': 1576, 'digest': '3498242c509e6e7f75ec5e00c1392f772ba0feca5596d60a35c7320f04744fea'}, 'package': 'internal/namespaceimport/ownedcontrol', 'topPASS': 1, 'subPASS': 3, 'skip': 0, 'fail': 0, 'namedCompletions': [{'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny/create-reply-error-and-shape-cannot-be-a-success'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny/describe-reply-error-and-shape-cannot-replace-exact-deny'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny/new-deny-visibility-is-bounded-without-grace-for-existing-denies'}], 'originalPackageFooterPASS': True, 'originalGoCommandPassed': False, 'originalTwoPackageScopeAccepted': False, 'producerPackageStarted': False, 'wholeOwnedcontrolPackagePASS': False, 'runtimeAccepted': False}, 'originalGoCommandPassed': False, 'originalWholeScopeAccepted': False, 'requiresExactSameSource': True, 'mayReuseOnlyKafkaNamedSubset': True}
COMPLETED_KAFKA_PACKAGE_GO = {'bytes': 17164055, 'sha256': '548d3c32f83fa81152f543eb1526be7884cfe0526eb5e9ce29d3eca14786e7e0', 'mode': 493}
COMPLETED_KAFKA_PACKAGE_REUSE = {'version': 'same-source-completed-kafka-package-reuse-v1', 'binding': {'schema': 1, 'kind': 'root-accepted-same-source-completed-kafka-package-in-timeout-command-v1', 'run': 42206676039, 'job': 127854974346, 'attempt': 1, 'head': 'cdd8f236614ba32f575367748b52137abf9bc63a', 'source': {'members': 1576, 'digest': '3498242c509e6e7f75ec5e00c1392f772ba0feca5596d60a35c7320f04744fea'}, 'rootReviewSHA256': '9b556b4d4ff96a6c52047803e9b713b7fb77548930a3ca0a4457f8a83b58edf0', 'artifactID': 13496927972, 'artifactSHA256': '7e6fa52a27545c51ec846235a498a9d3d1168b5aa786488cbfe85632805b70e6', 'eventsSHA256': '2c6b853aa4b39d1cabeb897e2b3f996deb57ab449621a8e1927dfad8574db06a', 'phaseSHA256': '9df468c689f414756aa6f9a5751d9583b941f8184795762cbc1d39126b6dc734', 'sourcePostSHA256': 'd3bbe21afff29a4e07926c3b53ce225790840422fd1fe7c7bde17e9b631a63fc', 'toolsSHA256': '162cbbdb16858edd435d19cdc43701020164c9248ca316909caddff7d875ee5f', 'originalPackageReceipt': {'source': {'members': 1576, 'digest': '3498242c509e6e7f75ec5e00c1392f772ba0feca5596d60a35c7320f04744fea'}, 'package': 'internal/namespaceimport/ownedcontrol', 'topPASS': 1, 'subPASS': 3, 'skip': 0, 'fail': 0, 'namedCompletions': [{'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny/create-reply-error-and-shape-cannot-be-a-success'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny/describe-reply-error-and-shape-cannot-replace-exact-deny'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny/new-deny-visibility-is-bounded-without-grace-for-existing-denies'}], 'originalPackageFooterPASS': True, 'originalGoCommandPassed': False, 'originalTwoPackageScopeAccepted': False, 'producerPackageStarted': False, 'wholeOwnedcontrolPackagePASS': False, 'runtimeAccepted': False}, 'originalGoCommandPassed': False, 'originalWholeScopeAccepted': False, 'requiresExactSameSource': True, 'mayReuseOnlyKafkaNamedSubset': True}, 'originalGoCommandPassed': False, 'originalTwoPackageScopeAccepted': False, 'actualCurrentGoCommandInvocations': 0, 'actualCurrentKafkaACLTestsDispatched': 0, 'reusedTopPASS': 1, 'reusedSubPASS': 3, 'wholeOwnedcontrolPackagePASS': False, 'wholeSourceAccepted': False, 'runtimeAccepted': False}
MANAGED_SHAPE_TWO_PACKAGE_PRIOR_ATTEMPT = {'kind': 'original-two-package-command-timeout-historical-attempt-v1', 'run': 42206676039, 'job': 127854974346, 'head': 'cdd8f236614ba32f575367748b52137abf9bc63a', 'source': {'members': 1576, 'digest': '3498242c509e6e7f75ec5e00c1392f772ba0feca5596d60a35c7320f04744fea'}, 'scope': {'phase': 'current-managed-shape-controls', 'targets': {'internal/namespaceimport/ownedcontrol': ['TestOwnedKafkaExclusionRequiresEveryWildcardDeny'], 'internal/msgtransfer': ['TestManagedOrdinaryProducerMatchesNamespaceLaunchCanonicalShape']}, 'requiredSubtests': {'internal/namespaceimport/ownedcontrol': {'TestOwnedKafkaExclusionRequiresEveryWildcardDeny': ['create-reply-error-and-shape-cannot-be-a-success', 'describe-reply-error-and-shape-cannot-replace-exact-deny', 'new-deny-visibility-is-bounded-without-grace-for-existing-denies']}, 'internal/msgtransfer': {'TestManagedOrdinaryProducerMatchesNamespaceLaunchCanonicalShape': ['producer-and-child-loader-roundtrip', 'alphabetic-map-reorder', 'unknown-field', 'missing-field', 'duplicate-field', 'wrong-value-type', 'null-string', 'wrong-pin', 'public-file', 'symlink-file', 'hardlink-file', 'trailing-json', 'missing-file']}}, 'goTimeoutSeconds': 180, 'phaseGuardSeconds': 240, 'topPASS': 2, 'subPASS': 16, 'packages': 2, 'goCommandInvocations': 1, 'packageExecutions': 2, 'samePackageInvocation': False, 'newDatabaseFixtures': 0, 'wholePackagePASS': False}, 'rootReviewSHA256': '9b556b4d4ff96a6c52047803e9b713b7fb77548930a3ca0a4457f8a83b58edf0', 'originalGoCommandPassed': False, 'originalTwoPackageScopeAccepted': False, 'producerPackageStarted': False}

def check_completed_kafka_package_reuse(value,go_tool=None):
 if json.dumps(value,sort_keys=True,separators=(',',':'))!=json.dumps(COMPLETED_KAFKA_PACKAGE_REUSE,sort_keys=True,separators=(',',':')):raise ValueError('exact Root accepted completed Kafka package only')
 if value['binding']['source']!=FINAL_SOURCE or value['binding']['originalPackageReceipt']['source']!=FINAL_SOURCE:raise ValueError('completed Kafka package requires exact current whole source')
 if go_tool is not None and {key:go_tool.get(key) for key in COMPLETED_KAFKA_PACKAGE_GO}!=COMPLETED_KAFKA_PACKAGE_GO:raise ValueError('same original Go tool body and mode required')
 return value

def check_current_producer_plan(c):
 control=CURRENT_PRODUCER_CONTROL;package=next(iter(control['targets']))
 if json.dumps(c.get('currentProducerControl'),sort_keys=True)!=json.dumps(control,sort_keys=True) or c.get('currentProducerTestsToRun')!=control['targets'][package] or c.get('kafkaACLTestsToRun')!=[] or c.get('processImageTestsToRun')!=[] or c.get('currentManagedShapeTestsToRun')!=[] or c.get('currentReleaseTestsToRun')!=[]:raise ValueError('fresh producer single-package scope only; never redispatch completed Kafka')
 if any(c.get(key) is not None for key in ('currentManagedShapeControl','currentReleaseControl','kafkaACLPassedReuse','currentImageKafkaControl','currentKafkaConfirmControl','kafkaConfirmPassedReuse')):raise ValueError('producer branch cannot alias any previous control or reuse branch')
 if json.dumps(c.get('sameSourceCompletedKafkaPackageReuse'),sort_keys=True)!=json.dumps(COMPLETED_KAFKA_PACKAGE_REUSE,sort_keys=True) or json.dumps(c.get('managedShapeTwoPackageHistoricalAttempt'),sort_keys=True)!=json.dumps(MANAGED_SHAPE_TWO_PACKAGE_PRIOR_ATTEMPT,sort_keys=True):raise ValueError('completed package and failed original whole-command facts required separately')
 check_completed_kafka_package_reuse(c['sameSourceCompletedKafkaPackageReuse'])
 if c.get('gatewayStartLogProjectionPolicy')!=GATEWAY_START_LOG_POLICY:raise ValueError('existing finite diagnostic policy differs')
 check_release_controls_prior_pass(c.get('releaseControlsHistoricalPass'));check_kafka_confirm_prior_pass(c.get('kafkaConfirmHistoricalPass'));check_kafka_acl_receipt(c.get('kafkaACLHistoricalPass'));check_process_image_prior(c.get('processImageHistoricalPass'))
 return control

def check_current_producer_receipt(value):
 keys={'topPASS','subPASS','skip','fail','packages','namedCompletions','source','accepted','newDatabaseFixtures','scope','actualGoCommandInvocations','actualPackageExecutions','actualKafkaACLTestsDispatched','actualManagedShapeTopsDispatched','actualProcessImageTestsDispatched','wholeMsgtransferPackagePASS','runtimeAccepted','wholeSourceAccepted'}
 control=CURRENT_PRODUCER_CONTROL
 if not isinstance(value,dict) or set(value)!=keys or value['source']!=FINAL_SOURCE or json.dumps(value['scope'],sort_keys=True)!=json.dumps(control,sort_keys=True) or value['accepted'] is not True or any(value[key] is not False for key in ('wholeMsgtransferPackagePASS','runtimeAccepted','wholeSourceAccepted')):raise ValueError('exact fresh producer named-subset receipt required')
 for key,want in [('topPASS',1),('subPASS',13),('skip',0),('fail',0),('packages',1),('newDatabaseFixtures',0),('actualGoCommandInvocations',1),('actualPackageExecutions',1),('actualKafkaACLTestsDispatched',0),('actualManagedShapeTopsDispatched',1),('actualProcessImageTestsDispatched',0)]:
  if type(value[key]) is not int or value[key]!=want:raise ValueError('exact producer-only command/package/subset counts required')
 package=next(iter(control['targets']));top=control['targets'][package][0];expected={(package,top)}|{(package,top+'/'+child) for child in control['requiredSubtests'][package][top]}
 names=value['namedCompletions']
 if not isinstance(names,list) or any(not isinstance(row,dict) or set(row)!={'package','name'} for row in names) or len(names)!=14 or {(row['package'],row['name']) for row in names}!=expected:raise ValueError('original producer top and all thirteen exact children required')
 return value

def run_current_producer_control(go,environment,command,parent,out,go_tool):
 reuse=check_completed_kafka_package_reuse(dict(COMPLETED_KAFKA_PACKAGE_REUSE),go_tool)
 write(out/'same-source-completed-kafka-package-reuse.json',reuse)
 write(out/'managed-shape-two-package-historical-attempt.json',MANAGED_SHAPE_TWO_PACKAGE_PRIOR_ATTEMPT)
 control=CURRENT_PRODUCER_CONTROL;package=next(iter(control['targets']));top=control['targets'][package][0]
 raw=command(control['phase'],[go,'test','-mod=readonly','-race','-p=1','-parallel=1','-count=1','-json','-vet=off','-timeout=180s','-run','^'+re.escape(top)+'$','./'+package],control['phaseGuardSeconds'],environment)
 events=[json.loads(line) for line in raw.decode().splitlines() if line.strip()];result=parent.audit(events,0,control['targets'])
 value=dict(result,source=FINAL_SOURCE,accepted=True,newDatabaseFixtures=0,scope=control,actualGoCommandInvocations=1,actualPackageExecutions=1,actualKafkaACLTestsDispatched=0,actualManagedShapeTopsDispatched=1,actualProcessImageTestsDispatched=0,wholeMsgtransferPackagePASS=False,runtimeAccepted=False,wholeSourceAccepted=False)
 check_current_producer_receipt(value);write(out/'current-managed-producer-control-names.json',value)
 write(out/'release-controls-historical-pass.json',check_release_controls_prior_pass(dict(RELEASE_CONTROLS_PRIOR_PASS)));write(out/'kafka-confirm-historical-pass.json',check_kafka_confirm_prior_pass(dict(KAFKA_CONFIRM_PRIOR_PASS)))
 write(out/'kafka-acl-historical-pass.json',check_kafka_acl_receipt(dict(KAFKA_ACL_PASSED_REUSE)));write(out/'process-image-historical-pass.json',check_process_image_prior(dict(PROCESS_IMAGE_PRIOR_PASS)))
 return value

def write_current_producer_summary(out,receipt,cleanup_audit,launcher_reuse,redis_parser_reuse):
 check_current_producer_receipt(receipt)
 write(out/'summary.json',{'accepted':True,'source':FINAL_SOURCE,'actualCurrentMainBuilds':14,'auxiliaryBuilds':2,'freshGraphScenarios':4,'pendingOriginalTopPASS':2,'pendingOriginalSubPASS':3,'targetedTopPASS':2,'targetedSubPASS':3,'managedProducerControlTopPASS':1,'managedProducerControlSubPASS':13,'managedProducerCommandInvocations':1,'managedProducerPackageExecutions':1,'actualKafkaACLTestsDispatched':0,'actualManagedShapeTopsDispatched':1,'currentProducerScope':receipt,'sameSourceCompletedKafkaPackageReuse':COMPLETED_KAFKA_PACKAGE_REUSE,'managedShapeTwoPackageHistoricalAttempt':MANAGED_SHAPE_TWO_PACKAGE_PRIOR_ATTEMPT,'currentWholeOwnedcontrolPackagePASS':False,'currentWholeMsgtransferPackagePASS':False,'actualProcessImageTestsDispatched':0,'processImageHistoricalPass':PROCESS_IMAGE_PRIOR_PASS,'releaseControlsHistoricalPass':RELEASE_CONTROLS_PRIOR_PASS,'kafkaConfirmHistoricalPass':KAFKA_CONFIRM_PRIOR_PASS,'kafkaConfirmPassedReuse':None,'kafkaACLPassedReuse':None,'kafkaACLHistoricalPass':KAFKA_ACL_PASSED_REUSE,'cleanupPassedReuse':CLEANUP_PASSED_REUSE,'cleanupRecentReuse':CLEANUP_RECENT_REUSE,'cleanupReuse':dict(CLEANUP_REUSE,topPASS=0,subPASS=1),'launcherPassedReuse':launcher_reuse,'actualLauncherTestsDispatched':0,'redisParserPassedReuse':redis_parser_reuse,'actualRedisParserTestsDispatched':0,'unaffectedHistoricalBatchesRedispatched':False,'historicalCleanupSourceFactsOnly':True,'originalObligation':{'topCount':5,'subCount':12,'crossSource':True},'originalNewSafetyContract':{'topCount':2,'subCount':4,'crossSource':True},'originalCompleteObligation':{'topCount':7,'subCount':16,'crossSource':True},'currentFreshGo':{'topPASS':3,'subPASS':16,'includesOriginalGraph':True},'reuseOldBinary':False,'deployment':False,'resourceCleanup':'must independently pass always collector','privateLogsUploaded':False})

CURRENT_GROUP_RECOVERY_CONTROL = {'phase': 'current-group-recovery-controls', 'targets': {'pkg/common/db/cache': ['TestGroupAuthorityInvalidatorOwnsFiniteClientWithoutChangingSharedOptions', 'TestGroupAuthorityInvalidatorRebuildsOwnedPoolWithoutMovingSourceOrLifetime'], 'internal/msgtransfer': ['TestManagedOrdinaryProducerMatchesNamespaceLaunchCanonicalShape'], 'internal/namespaceimport/ownedcontrol': ['TestOwnedKafkaExclusionRequiresEveryWildcardDeny']}, 'requiredSubtests': {'pkg/common/db/cache': {'TestGroupAuthorityInvalidatorRebuildsOwnedPoolWithoutMovingSourceOrLifetime': ['single-node-independent-options-and-close', 'cluster-independent-options-and-close', 'closing-base-keeps-existing-recovery', 'actual-driver-ack-keeps-original-owner', 'actual-driver-lost-ack-retires-only-recovery', 'parent-cancel-refuses-rebuild', 'foreign-and-malformed-owned-clients-refused']}, 'internal/msgtransfer': {'TestManagedOrdinaryProducerMatchesNamespaceLaunchCanonicalShape': ['producer-and-child-loader-roundtrip', 'alphabetic-map-reorder', 'unknown-field', 'missing-field', 'duplicate-field', 'wrong-value-type', 'null-string', 'wrong-pin', 'public-file', 'symlink-file', 'hardlink-file', 'trailing-json', 'missing-file']}, 'internal/namespaceimport/ownedcontrol': {'TestOwnedKafkaExclusionRequiresEveryWildcardDeny': ['create-reply-error-and-shape-cannot-be-a-success', 'describe-reply-error-and-shape-cannot-replace-exact-deny', 'new-deny-visibility-is-bounded-without-grace-for-existing-denies']}}, 'goTimeoutSeconds': 180, 'phaseGuardSeconds': 600, 'topPASS': 4, 'subPASS': 23, 'packages': 3, 'goCommandInvocations': 1, 'packageExecutions': 3, 'samePackageInvocation': False, 'newDatabaseFixtures': 0, 'wholePackagePASS': False}
GROUP_RECOVERY_HISTORICAL_FACTS = {'kind': 'group-recovery-original-349-controls-historical-facts-v1', 'source': {'members': 1576, 'digest': '3498242c509e6e7f75ec5e00c1392f772ba0feca5596d60a35c7320f04744fea'}, 'completedKafkaPackage': {'version': 'same-source-completed-kafka-package-reuse-v1', 'binding': {'schema': 1, 'kind': 'root-accepted-same-source-completed-kafka-package-in-timeout-command-v1', 'run': 42206676039, 'job': 127854974346, 'attempt': 1, 'head': 'cdd8f236614ba32f575367748b52137abf9bc63a', 'source': {'members': 1576, 'digest': '3498242c509e6e7f75ec5e00c1392f772ba0feca5596d60a35c7320f04744fea'}, 'rootReviewSHA256': '9b556b4d4ff96a6c52047803e9b713b7fb77548930a3ca0a4457f8a83b58edf0', 'artifactID': 13496927972, 'artifactSHA256': '7e6fa52a27545c51ec846235a498a9d3d1168b5aa786488cbfe85632805b70e6', 'eventsSHA256': '2c6b853aa4b39d1cabeb897e2b3f996deb57ab449621a8e1927dfad8574db06a', 'phaseSHA256': '9df468c689f414756aa6f9a5751d9583b941f8184795762cbc1d39126b6dc734', 'sourcePostSHA256': 'd3bbe21afff29a4e07926c3b53ce225790840422fd1fe7c7bde17e9b631a63fc', 'toolsSHA256': '162cbbdb16858edd435d19cdc43701020164c9248ca316909caddff7d875ee5f', 'originalPackageReceipt': {'source': {'members': 1576, 'digest': '3498242c509e6e7f75ec5e00c1392f772ba0feca5596d60a35c7320f04744fea'}, 'package': 'internal/namespaceimport/ownedcontrol', 'topPASS': 1, 'subPASS': 3, 'skip': 0, 'fail': 0, 'namedCompletions': [{'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny/create-reply-error-and-shape-cannot-be-a-success'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny/describe-reply-error-and-shape-cannot-replace-exact-deny'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny/new-deny-visibility-is-bounded-without-grace-for-existing-denies'}], 'originalPackageFooterPASS': True, 'originalGoCommandPassed': False, 'originalTwoPackageScopeAccepted': False, 'producerPackageStarted': False, 'wholeOwnedcontrolPackagePASS': False, 'runtimeAccepted': False}, 'originalGoCommandPassed': False, 'originalWholeScopeAccepted': False, 'requiresExactSameSource': True, 'mayReuseOnlyKafkaNamedSubset': True}, 'originalGoCommandPassed': False, 'originalTwoPackageScopeAccepted': False, 'actualCurrentGoCommandInvocations': 0, 'actualCurrentKafkaACLTestsDispatched': 0, 'reusedTopPASS': 1, 'reusedSubPASS': 3, 'wholeOwnedcontrolPackagePASS': False, 'wholeSourceAccepted': False, 'runtimeAccepted': False}, 'producerEventGap': {'run': 42208550310, 'job': 127861195844, 'head': 'd1f835974da5ccae3ec3e17f98816560084c422f', 'rootReviewSHA256': '07c159206a79fb494dd182dc6ba38f6f368c1fddfb192e0b8f2b5789dfd4ddc8', 'phaseExitCode': 0, 'namedReceipt': {'topPASS': 1, 'subPASS': 13, 'skip': 0, 'fail': 0, 'packages': 1, 'namedCompletions': [{'package': 'internal/msgtransfer', 'name': 'TestManagedOrdinaryProducerMatchesNamespaceLaunchCanonicalShape'}, {'package': 'internal/msgtransfer', 'name': 'TestManagedOrdinaryProducerMatchesNamespaceLaunchCanonicalShape/alphabetic-map-reorder'}, {'package': 'internal/msgtransfer', 'name': 'TestManagedOrdinaryProducerMatchesNamespaceLaunchCanonicalShape/duplicate-field'}, {'package': 'internal/msgtransfer', 'name': 'TestManagedOrdinaryProducerMatchesNamespaceLaunchCanonicalShape/hardlink-file'}, {'package': 'internal/msgtransfer', 'name': 'TestManagedOrdinaryProducerMatchesNamespaceLaunchCanonicalShape/missing-field'}, {'package': 'internal/msgtransfer', 'name': 'TestManagedOrdinaryProducerMatchesNamespaceLaunchCanonicalShape/missing-file'}, {'package': 'internal/msgtransfer', 'name': 'TestManagedOrdinaryProducerMatchesNamespaceLaunchCanonicalShape/null-string'}, {'package': 'internal/msgtransfer', 'name': 'TestManagedOrdinaryProducerMatchesNamespaceLaunchCanonicalShape/producer-and-child-loader-roundtrip'}, {'package': 'internal/msgtransfer', 'name': 'TestManagedOrdinaryProducerMatchesNamespaceLaunchCanonicalShape/public-file'}, {'package': 'internal/msgtransfer', 'name': 'TestManagedOrdinaryProducerMatchesNamespaceLaunchCanonicalShape/symlink-file'}, {'package': 'internal/msgtransfer', 'name': 'TestManagedOrdinaryProducerMatchesNamespaceLaunchCanonicalShape/trailing-json'}, {'package': 'internal/msgtransfer', 'name': 'TestManagedOrdinaryProducerMatchesNamespaceLaunchCanonicalShape/unknown-field'}, {'package': 'internal/msgtransfer', 'name': 'TestManagedOrdinaryProducerMatchesNamespaceLaunchCanonicalShape/wrong-pin'}, {'package': 'internal/msgtransfer', 'name': 'TestManagedOrdinaryProducerMatchesNamespaceLaunchCanonicalShape/wrong-value-type'}], 'source': {'members': 1576, 'digest': '3498242c509e6e7f75ec5e00c1392f772ba0feca5596d60a35c7320f04744fea'}, 'accepted': True, 'newDatabaseFixtures': 0, 'scope': {'phase': 'current-managed-producer-control', 'targets': {'internal/msgtransfer': ['TestManagedOrdinaryProducerMatchesNamespaceLaunchCanonicalShape']}, 'requiredSubtests': {'internal/msgtransfer': {'TestManagedOrdinaryProducerMatchesNamespaceLaunchCanonicalShape': ['producer-and-child-loader-roundtrip', 'alphabetic-map-reorder', 'unknown-field', 'missing-field', 'duplicate-field', 'wrong-value-type', 'null-string', 'wrong-pin', 'public-file', 'symlink-file', 'hardlink-file', 'trailing-json', 'missing-file']}}, 'goTimeoutSeconds': 180, 'phaseGuardSeconds': 600, 'topPASS': 1, 'subPASS': 13, 'packages': 1, 'goCommandInvocations': 1, 'packageExecutions': 1, 'newDatabaseFixtures': 0, 'wholePackagePASS': False}, 'actualGoCommandInvocations': 1, 'actualPackageExecutions': 1, 'actualKafkaACLTestsDispatched': 0, 'actualManagedShapeTopsDispatched': 1, 'actualProcessImageTestsDispatched': 0, 'wholeMsgtransferPackagePASS': False, 'runtimeAccepted': False, 'wholeSourceAccepted': False}, 'namedReceiptPin': {'bytes': 2882, 'sha256': '5809d73ecbc5e25bb8a8efd73b505b874b708233220856ae3a148a7ff334ffb3'}, 'independentEventsPresent': False, 'collectorAccepted': False, 'mayReuseAsCurrentControl': False}, 'currentSourcePASS': False, 'wholePackagePASS': False, 'runtimeAccepted': False}
RPC_GROUP_START_LOG_POLICY = {'version': 'owned-rpc-group-start-log-projection-v1', 'logLimitBytes': 8388608, 'lineLimit': 16384, 'lineBytesLimit': 4096, 'framesLimit': 8, 'directoryEntriesLimit': 64, 'onlyRole': 'rpc-group', 'onlyCachedWaitExitCode': 2, 'rawTextExported': False, 'acceptanceEvidence': False, 'panicPrefixClassesLimit': 8, 'wrapperLimit': 8}

def check_group_recovery_historical_facts(value):
 if json.dumps(value,sort_keys=True,separators=(',',':'))!=json.dumps(GROUP_RECOVERY_HISTORICAL_FACTS,sort_keys=True,separators=(',',':')) or value['source']==FINAL_SOURCE:raise ValueError('original 349 Kafka subset and producer event gap are historical only')
 return value

def check_current_group_recovery_plan(c):
 control=CURRENT_GROUP_RECOVERY_CONTROL;owned='internal/namespaceimport/ownedcontrol'
 if json.dumps(c.get('currentGroupRecoveryControl'),sort_keys=True)!=json.dumps(control,sort_keys=True) or c.get('currentGroupRecoveryTargets')!=control['targets'] or c.get('kafkaACLTestsToRun')!=control['targets'][owned]:raise ValueError('exact fresh cache/producer/Kafka three-package scope required')
 if any(c.get(key) is not None for key in ('currentProducerControl','sameSourceCompletedKafkaPackageReuse','currentManagedShapeControl','currentReleaseControl','kafkaACLPassedReuse','currentImageKafkaControl','currentKafkaConfirmControl','kafkaConfirmPassedReuse')) or any(c.get(key)!=[] for key in ('currentProducerTestsToRun','currentManagedShapeTestsToRun','currentReleaseTestsToRun','processImageTestsToRun')):raise ValueError('new source requires all four named tops fresh without prior-source reuse')
 if c.get('gatewayStartLogProjectionPolicy')!=GATEWAY_START_LOG_POLICY or c.get('rpcGroupStartLogProjectionPolicy')!=RPC_GROUP_START_LOG_POLICY:raise ValueError('exact bounded cached-Wait diagnostic policies required')
 check_group_recovery_historical_facts(c.get('groupRecoveryHistoricalFacts'));check_release_controls_prior_pass(c.get('releaseControlsHistoricalPass'));check_kafka_confirm_prior_pass(c.get('kafkaConfirmHistoricalPass'));check_kafka_acl_receipt(c.get('kafkaACLHistoricalPass'));check_process_image_prior(c.get('processImageHistoricalPass'))
 return control

def group_recovery_package_facts(value):
 control=CURRENT_GROUP_RECOVERY_CONTROL
 return {package:{'source':FINAL_SOURCE,'topPASS':len(tops),'subPASS':sum(len(control['requiredSubtests'][package].get(top,[])) for top in tops),'namedCompletions':sorted([dict(row) for row in value['namedCompletions'] if row['package']==package],key=lambda row:row['name']),'withinCommand':control['phase'],'packageExecutions':1,'wholePackagePASS':False,'runtimeAccepted':False} for package,tops in control['targets'].items()}

def check_current_group_recovery_receipt(value):
 keys={'topPASS','subPASS','skip','fail','packages','namedCompletions','source','accepted','newDatabaseFixtures','scope','actualGoCommandInvocations','actualPackageExecutions','actualKafkaACLTestsDispatched','actualManagedShapeTopsDispatched','actualCacheTopsDispatched','actualProcessImageTestsDispatched','packageFacts','runtimeAccepted','wholeSourceAccepted'}
 control=CURRENT_GROUP_RECOVERY_CONTROL
 if not isinstance(value,dict) or set(value)!=keys or value['source']!=FINAL_SOURCE or json.dumps(value['scope'],sort_keys=True)!=json.dumps(control,sort_keys=True) or value['accepted'] is not True or value['runtimeAccepted'] is not False or value['wholeSourceAccepted'] is not False:raise ValueError('exact fresh three-package named-subset receipt required')
 for key,want in [('topPASS',4),('subPASS',23),('skip',0),('fail',0),('packages',3),('newDatabaseFixtures',0),('actualGoCommandInvocations',1),('actualPackageExecutions',3),('actualKafkaACLTestsDispatched',1),('actualManagedShapeTopsDispatched',1),('actualCacheTopsDispatched',2),('actualProcessImageTestsDispatched',0)]:
  if type(value[key]) is not int or value[key]!=want:raise ValueError('exact original command/package/subset counts required')
 expected={(package,top) for package,tops in control['targets'].items() for top in tops}|{(package,top+'/'+child) for package,parents in control['requiredSubtests'].items() for top,children in parents.items() for child in children}
 names=value['namedCompletions']
 if not isinstance(names,list) or len(names)!=27 or any(not isinstance(row,dict) or set(row)!={'package','name'} for row in names) or {(row['package'],row['name']) for row in names}!=expected or json.dumps(value['packageFacts'],sort_keys=True)!=json.dumps(group_recovery_package_facts(value),sort_keys=True):raise ValueError('all four exact tops/twenty-three children and original package facts required')
 return value

def run_current_group_recovery_control(go,environment,command,parent,out):
 control=CURRENT_GROUP_RECOVERY_CONTROL;tops=[top for names in control['targets'].values() for top in names];pattern='^('+'|'.join(re.escape(top) for top in tops)+')$'
 raw=command(control['phase'],[go,'test','-mod=readonly','-race','-p=1','-parallel=1','-count=1','-json','-vet=off','-timeout=180s','-run',pattern]+['./'+package for package in control['targets']],control['phaseGuardSeconds'],environment)
 events=[json.loads(line) for line in raw.decode().splitlines() if line.strip()];result=parent.audit(events,0,control['targets'])
 value=dict(result,source=FINAL_SOURCE,accepted=True,newDatabaseFixtures=0,scope=control,actualGoCommandInvocations=1,actualPackageExecutions=3,actualKafkaACLTestsDispatched=1,actualManagedShapeTopsDispatched=1,actualCacheTopsDispatched=2,actualProcessImageTestsDispatched=0,runtimeAccepted=False,wholeSourceAccepted=False)
 value['packageFacts']=group_recovery_package_facts(value);check_current_group_recovery_receipt(value);write(out/'current-group-recovery-controls-names.json',value)
 write(out/'group-recovery-historical-facts.json',check_group_recovery_historical_facts(dict(GROUP_RECOVERY_HISTORICAL_FACTS)))
 write(out/'release-controls-historical-pass.json',check_release_controls_prior_pass(dict(RELEASE_CONTROLS_PRIOR_PASS)));write(out/'kafka-confirm-historical-pass.json',check_kafka_confirm_prior_pass(dict(KAFKA_CONFIRM_PRIOR_PASS)))
 write(out/'kafka-acl-historical-pass.json',check_kafka_acl_receipt(dict(KAFKA_ACL_PASSED_REUSE)));write(out/'process-image-historical-pass.json',check_process_image_prior(dict(PROCESS_IMAGE_PRIOR_PASS)))
 return value

def write_current_group_recovery_summary(out,receipt,cleanup_audit,launcher_reuse,redis_parser_reuse):
 check_current_group_recovery_receipt(receipt)
 write(out/'summary.json',{'accepted':True,'source':FINAL_SOURCE,'actualCurrentMainBuilds':14,'auxiliaryBuilds':2,'freshGraphScenarios':4,'pendingOriginalTopPASS':2,'pendingOriginalSubPASS':3,'targetedTopPASS':2,'targetedSubPASS':3,'groupRecoveryControlTopPASS':4,'groupRecoveryControlSubPASS':23,'groupRecoveryCommandInvocations':1,'groupRecoveryPackageExecutions':3,'actualKafkaACLTestsDispatched':1,'actualManagedShapeTopsDispatched':1,'actualCacheTopsDispatched':2,'currentGroupRecoveryScope':receipt,'packageFacts':receipt['packageFacts'],'groupRecoveryHistoricalFacts':GROUP_RECOVERY_HISTORICAL_FACTS,'currentWholeOwnedcontrolPackagePASS':False,'currentWholeMsgtransferPackagePASS':False,'currentWholeCachePackagePASS':False,'actualProcessImageTestsDispatched':0,'processImageHistoricalPass':PROCESS_IMAGE_PRIOR_PASS,'releaseControlsHistoricalPass':RELEASE_CONTROLS_PRIOR_PASS,'kafkaConfirmHistoricalPass':KAFKA_CONFIRM_PRIOR_PASS,'kafkaConfirmPassedReuse':None,'kafkaACLPassedReuse':None,'kafkaACLHistoricalPass':KAFKA_ACL_PASSED_REUSE,'cleanupPassedReuse':CLEANUP_PASSED_REUSE,'cleanupRecentReuse':CLEANUP_RECENT_REUSE,'cleanupReuse':dict(CLEANUP_REUSE,topPASS=0,subPASS=1),'launcherPassedReuse':launcher_reuse,'actualLauncherTestsDispatched':0,'redisParserPassedReuse':redis_parser_reuse,'actualRedisParserTestsDispatched':0,'unaffectedHistoricalBatchesRedispatched':False,'historicalCleanupSourceFactsOnly':True,'originalObligation':{'topCount':5,'subCount':12,'crossSource':True},'originalNewSafetyContract':{'topCount':2,'subCount':4,'crossSource':True},'originalCompleteObligation':{'topCount':7,'subCount':16,'crossSource':True},'currentFreshGo':{'topPASS':6,'subPASS':26,'includesOriginalGraph':True},'reuseOldBinary':False,'deployment':False,'resourceCleanup':'must independently pass always collector','privateLogsUploaded':False})

CURRENT_CACHE_TEST_DELTA_CONTROL = {'phase': 'current-cache-test-delta-controls', 'targets': {'pkg/common/db/cache': ['TestGroupAuthorityInvalidatorOwnsFiniteClientWithoutChangingSharedOptions', 'TestGroupAuthorityInvalidatorRebuildsOwnedPoolWithoutMovingSourceOrLifetime']}, 'requiredSubtests': {'pkg/common/db/cache': {'TestGroupAuthorityInvalidatorRebuildsOwnedPoolWithoutMovingSourceOrLifetime': ['single-node-independent-options-and-close', 'cluster-independent-options-and-close', 'closing-base-keeps-existing-recovery', 'actual-driver-ack-keeps-original-owner', 'actual-driver-lost-ack-retires-only-recovery', 'parent-cancel-refuses-rebuild', 'foreign-and-malformed-owned-clients-refused']}}, 'goTimeoutSeconds': 180, 'phaseGuardSeconds': 600, 'topPASS': 2, 'subPASS': 7, 'packages': 1, 'goCommandInvocations': 1, 'packageExecutions': 1, 'newDatabaseFixtures': 0, 'wholePackagePASS': False}
CACHE_TEST_DELTA_PACKAGE_REUSE = {'version': 'exact-cache-test-delta-completed-package-compatibility-v1', 'originalBinding': {'schema': 1, 'kind': 'root-accepted-two-completed-packages-in-failed-cache-command-v1', 'run': 42215107940, 'job': 127883050593, 'head': '1ef9d4920278da3aa407a2c8f6d4fc37fd12896b', 'attempt': 1, 'source': {'members': 1576, 'digest': 'bff341cc7732a07aecea9e14254d53d7bdd509a4f537b4cf414e079f1c248c08'}, 'rootReviewSHA256': '3b0ff325cd616925e89c1d36b69a2c3e9f4a8c92469195171b14eded0837d02f', 'artifactID': 13501560279, 'artifactSHA256': '170825c7e45f76c38a9af6ace929f13464512823850ae339d8936af27140389b', 'eventsSHA256': '5830ce62ab57d7560dce479259cc5356309a08ae867cf4e1e46df63915eaee81', 'phaseSHA256': '489ea6a494e0ba87d4d4659418eb1676161e7dcc47945148870dee31416aeebd', 'sourcePostSHA256': 'ec0213a4e905f6acdd29b214d0371027e50b16014876e1407f9a202bd2f36824', 'toolsSHA256': '162cbbdb16858edd435d19cdc43701020164c9248ca316909caddff7d875ee5f', 'GoToolBodyAndMode': {'bytes': 17164055, 'sha256': '548d3c32f83fa81152f543eb1526be7884cfe0526eb5e9ce29d3eca14786e7e0', 'mode': 493}, 'completedPackages': {'internal/msgtransfer': {'source': {'members': 1576, 'digest': 'bff341cc7732a07aecea9e14254d53d7bdd509a4f537b4cf414e079f1c248c08'}, 'topPASS': 1, 'subPASS': 13, 'skip': 0, 'fail': 0, 'namedCompletions': [{'package': 'internal/msgtransfer', 'name': 'TestManagedOrdinaryProducerMatchesNamespaceLaunchCanonicalShape'}, {'package': 'internal/msgtransfer', 'name': 'TestManagedOrdinaryProducerMatchesNamespaceLaunchCanonicalShape/alphabetic-map-reorder'}, {'package': 'internal/msgtransfer', 'name': 'TestManagedOrdinaryProducerMatchesNamespaceLaunchCanonicalShape/duplicate-field'}, {'package': 'internal/msgtransfer', 'name': 'TestManagedOrdinaryProducerMatchesNamespaceLaunchCanonicalShape/hardlink-file'}, {'package': 'internal/msgtransfer', 'name': 'TestManagedOrdinaryProducerMatchesNamespaceLaunchCanonicalShape/missing-field'}, {'package': 'internal/msgtransfer', 'name': 'TestManagedOrdinaryProducerMatchesNamespaceLaunchCanonicalShape/missing-file'}, {'package': 'internal/msgtransfer', 'name': 'TestManagedOrdinaryProducerMatchesNamespaceLaunchCanonicalShape/null-string'}, {'package': 'internal/msgtransfer', 'name': 'TestManagedOrdinaryProducerMatchesNamespaceLaunchCanonicalShape/producer-and-child-loader-roundtrip'}, {'package': 'internal/msgtransfer', 'name': 'TestManagedOrdinaryProducerMatchesNamespaceLaunchCanonicalShape/public-file'}, {'package': 'internal/msgtransfer', 'name': 'TestManagedOrdinaryProducerMatchesNamespaceLaunchCanonicalShape/symlink-file'}, {'package': 'internal/msgtransfer', 'name': 'TestManagedOrdinaryProducerMatchesNamespaceLaunchCanonicalShape/trailing-json'}, {'package': 'internal/msgtransfer', 'name': 'TestManagedOrdinaryProducerMatchesNamespaceLaunchCanonicalShape/unknown-field'}, {'package': 'internal/msgtransfer', 'name': 'TestManagedOrdinaryProducerMatchesNamespaceLaunchCanonicalShape/wrong-pin'}, {'package': 'internal/msgtransfer', 'name': 'TestManagedOrdinaryProducerMatchesNamespaceLaunchCanonicalShape/wrong-value-type'}], 'originalPackageFooterPASS': True, 'wholePackagePASS': False, 'runtimeAccepted': False}, 'internal/namespaceimport/ownedcontrol': {'source': {'members': 1576, 'digest': 'bff341cc7732a07aecea9e14254d53d7bdd509a4f537b4cf414e079f1c248c08'}, 'topPASS': 1, 'subPASS': 3, 'skip': 0, 'fail': 0, 'namedCompletions': [{'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny/create-reply-error-and-shape-cannot-be-a-success'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny/describe-reply-error-and-shape-cannot-replace-exact-deny'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedKafkaExclusionRequiresEveryWildcardDeny/new-deny-visibility-is-bounded-without-grace-for-existing-denies'}], 'originalPackageFooterPASS': True, 'wholePackagePASS': False, 'runtimeAccepted': False}}, 'originalGoCommandPassed': False, 'originalWholeScopeAccepted': False, 'cachePackagePassed': False, 'sameWholeSourceForLaterTestDelta': False, 'onlyExplicitExactTestDeltaCompatibilityMayConsume': True, 'currentFreshCacheRequired': True, 'runtimeAccepted': False}, 'currentSource': {'members': 1576, 'digest': 'f2de06c0b25c49735db974b9a726adbfae66612a4b3f35962b9e1a73207d3e6f'}, 'originalSource': {'members': 1576, 'digest': 'bff341cc7732a07aecea9e14254d53d7bdd509a4f537b4cf414e079f1c248c08'}, 'delta': {'target': 'server/pkg/common/db/cache/group_authority_invalidator_test.go', 'before': {'bytes': 12325, 'sha256': 'b19daee1eb934bd08e079487f730b0da9f125dc6d5bdb528695b28290a9427c6'}, 'after': {'bytes': 12326, 'sha256': '21d806aef240c2953b682848dbb622e44e1c86ea2a730d3603e13416f72115e9'}, 'contextStart': '\tt.Run("cluster-independent-options-and-close", func(t *testing.T) {', 'contextEnd': '\tt.Run("closing-base-keeps-existing-recovery", func(t *testing.T) {', 'oldLiteral': 'opts.MaxRetries != 0', 'newLiteral': 'opts.MaxRetries != -1', 'occurrences': 1}, 'sameWholeSource': False, 'originalGoCommandPassed': False, 'originalWholeScopeAccepted': False, 'oldCachePackagePassed': False, 'compatibilityOnlyForNamedCompletedPackages': True, 'actualCurrentProducerCommands': 0, 'actualCurrentKafkaCommands': 0, 'actualCurrentPackageExecutionsForReusedFacts': 0, 'runtimeAccepted': False, 'wholeSourceAccepted': False, 'wholePackagePASS': False}

def check_cache_test_delta_plan(c):
 control=CURRENT_CACHE_TEST_DELTA_CONTROL;package=next(iter(control['targets']))
 if json.dumps(c.get('currentCacheTestDeltaControl'),sort_keys=True)!=json.dumps(control,sort_keys=True) or c.get('cacheTestDeltaTestsToRun')!=control['targets'][package] or json.dumps(c.get('cacheTestDeltaPackageReuse'),sort_keys=True)!=json.dumps(CACHE_TEST_DELTA_PACKAGE_REUSE,sort_keys=True):raise ValueError('exact cache-only fresh scope and explicit test-delta compatibility required')
 if any(c.get(key) is not None for key in ('currentGroupRecoveryControl','currentProducerControl','sameSourceCompletedKafkaPackageReuse','currentManagedShapeControl','currentReleaseControl','kafkaACLPassedReuse','currentImageKafkaControl','currentKafkaConfirmControl','kafkaConfirmPassedReuse')) or any(c.get(key)!=[] for key in ('kafkaACLTestsToRun','currentProducerTestsToRun','currentManagedShapeTestsToRun','currentReleaseTestsToRun','processImageTestsToRun')):raise ValueError('completed producer/Kafka packages cannot be redispatched or alias older scopes')
 if c.get('gatewayStartLogProjectionPolicy')!=GATEWAY_START_LOG_POLICY or c.get('rpcGroupStartLogProjectionPolicy')!=RPC_GROUP_START_LOG_POLICY:raise ValueError('unchanged bounded startup policies required')
 check_group_recovery_historical_facts(c.get('groupRecoveryHistoricalFacts'));check_release_controls_prior_pass(c.get('releaseControlsHistoricalPass'));check_kafka_confirm_prior_pass(c.get('kafkaConfirmHistoricalPass'));check_kafka_acl_receipt(c.get('kafkaACLHistoricalPass'));check_process_image_prior(c.get('processImageHistoricalPass'))
 return control

def check_cache_test_delta_reuse(value,bodies,recipe,go_tool=None):
 if json.dumps(value,sort_keys=True,separators=(',',':'))!=json.dumps(CACHE_TEST_DELTA_PACKAGE_REUSE,sort_keys=True,separators=(',',':')) or value['currentSource']!=FINAL_SOURCE or value['originalSource']==FINAL_SOURCE:raise ValueError('exact cross-source test-delta receipt only; never same whole source')
 if len(bodies)!=FINAL_SOURCE['members'] or recipe.digest(recipe.rows(bodies))!=FINAL_SOURCE['digest']:raise ValueError('complete exact current vector required before compatibility')
 delta=value['delta'];raw=bodies.get(delta['target']);identity=delta['after']
 if not isinstance(raw,bytes) or len(raw)!=identity['bytes'] or sha(raw)!=identity['sha256']:raise ValueError('one exact formatted cache test postimage required')
 begin,end=delta['contextStart'].encode(),delta['contextEnd'].encode();old,new=delta['oldLiteral'].encode(),delta['newLiteral'].encode()
 if raw.count(begin)!=1 or raw.count(end)!=1:raise ValueError('unique fixed cluster child boundaries required')
 left,right=raw.index(begin),raw.index(end)
 if left>=right or raw[left:right].count(new)!=1 or old in raw[left:right]:raise ValueError('one new cluster expectation in its original child required')
 prior=raw[:left]+raw[left:right].replace(new,old,1)+raw[right:];before=delta['before']
 if len(prior)!=before['bytes'] or sha(prior)!=before['sha256']:raise ValueError('single literal reversal must recreate exact original test bytes')
 previous=dict(bodies);previous[delta['target']]=prior
 if len(previous)!=value['originalSource']['members'] or recipe.digest(recipe.rows(previous))!=value['originalSource']['digest']:raise ValueError('all other production, lock, test and dependency bytes must reconstruct original BFF vector')
 # A wide embed could otherwise turn a test-only edit into production data.
 # The reviewed completed tops use only fixed response closures/private temp
 # JSON, never this source file. Other packages cannot embed through '..'.
 for name,body in bodies.items():
  if name.startswith('server/pkg/common/db/cache/') and '/' not in name[len('server/pkg/common/db/cache/'):] and name.endswith('.go') and not name.endswith('_test.go'):
   if re.search(rb'(?m)^[ \t]*//go:embed(?:[ \t]|$)',body) or re.search(rb'\b(?:os|ioutil)\.(?:ReadFile|ReadAll|Open|OpenFile)\s*\(|\bruntime\.Caller\s*\(',body):raise ValueError('cache production source-data dependency is incompatible with this bridge')
 if go_tool is not None:
  want=value['originalBinding']['GoToolBodyAndMode']
  if json.dumps({key:go_tool.get(key) for key in want},sort_keys=True)!=json.dumps(want,sort_keys=True):raise ValueError('original Go tool body and mode required for completed package compatibility')
 return value

def check_current_cache_test_delta_receipt(value):
 keys={'topPASS','subPASS','skip','fail','packages','namedCompletions','source','accepted','newDatabaseFixtures','scope','actualGoCommandInvocations','actualPackageExecutions','actualKafkaACLTestsDispatched','actualManagedShapeTopsDispatched','actualCacheTopsDispatched','actualProcessImageTestsDispatched','wholeCachePackagePASS','runtimeAccepted','wholeSourceAccepted'}
 control=CURRENT_CACHE_TEST_DELTA_CONTROL
 if not isinstance(value,dict) or set(value)!=keys or value['source']!=FINAL_SOURCE or json.dumps(value['scope'],sort_keys=True)!=json.dumps(control,sort_keys=True) or value['accepted'] is not True or any(value[key] is not False for key in ('wholeCachePackagePASS','runtimeAccepted','wholeSourceAccepted')):raise ValueError('exact fresh cache named subset receipt only')
 for key,want in [('topPASS',2),('subPASS',7),('skip',0),('fail',0),('packages',1),('newDatabaseFixtures',0),('actualGoCommandInvocations',1),('actualPackageExecutions',1),('actualKafkaACLTestsDispatched',0),('actualManagedShapeTopsDispatched',0),('actualCacheTopsDispatched',2),('actualProcessImageTestsDispatched',0)]:
  if type(value[key]) is not int or value[key]!=want:raise ValueError('exact cache-only command/package counts required')
 package=next(iter(control['targets']));expected={(package,top) for top in control['targets'][package]}|{(package,top+'/'+child) for top,children in control['requiredSubtests'][package].items() for child in children};names=value['namedCompletions']
 if not isinstance(names,list) or len(names)!=9 or any(not isinstance(row,dict) or set(row)!={'package','name'} for row in names) or {(row['package'],row['name']) for row in names}!=expected:raise ValueError('both exact cache tops and all seven children required')
 return value

def run_current_cache_test_delta_control(go,environment,command,parent,out,bodies,recipe,go_tool):
 reuse=check_cache_test_delta_reuse(dict(CACHE_TEST_DELTA_PACKAGE_REUSE),bodies,recipe,go_tool);write(out/'completed-package-test-delta-reuse.json',reuse)
 control=CURRENT_CACHE_TEST_DELTA_CONTROL;package=next(iter(control['targets']));pattern='^('+'|'.join(re.escape(top) for top in control['targets'][package])+')$'
 raw=command(control['phase'],[go,'test','-mod=readonly','-race','-p=1','-parallel=1','-count=1','-json','-vet=off','-timeout=180s','-run',pattern,'./'+package],control['phaseGuardSeconds'],environment)
 events=[json.loads(line) for line in raw.decode().splitlines() if line.strip()];result=parent.audit(events,0,control['targets'])
 value=dict(result,source=FINAL_SOURCE,accepted=True,newDatabaseFixtures=0,scope=control,actualGoCommandInvocations=1,actualPackageExecutions=1,actualKafkaACLTestsDispatched=0,actualManagedShapeTopsDispatched=0,actualCacheTopsDispatched=2,actualProcessImageTestsDispatched=0,wholeCachePackagePASS=False,runtimeAccepted=False,wholeSourceAccepted=False)
 check_current_cache_test_delta_receipt(value);write(out/'current-cache-test-delta-controls-names.json',value)
 write(out/'group-recovery-historical-facts.json',check_group_recovery_historical_facts(dict(GROUP_RECOVERY_HISTORICAL_FACTS)))
 write(out/'release-controls-historical-pass.json',check_release_controls_prior_pass(dict(RELEASE_CONTROLS_PRIOR_PASS)));write(out/'kafka-confirm-historical-pass.json',check_kafka_confirm_prior_pass(dict(KAFKA_CONFIRM_PRIOR_PASS)))
 write(out/'kafka-acl-historical-pass.json',check_kafka_acl_receipt(dict(KAFKA_ACL_PASSED_REUSE)));write(out/'process-image-historical-pass.json',check_process_image_prior(dict(PROCESS_IMAGE_PRIOR_PASS)))
 return value

def write_current_cache_test_delta_summary(out,receipt,cleanup_audit,launcher_reuse,redis_parser_reuse):
 check_current_cache_test_delta_receipt(receipt)
 write(out/'summary.json',{'accepted':True,'source':FINAL_SOURCE,'actualCurrentMainBuilds':14,'auxiliaryBuilds':2,'freshGraphScenarios':4,'pendingOriginalTopPASS':2,'pendingOriginalSubPASS':3,'targetedTopPASS':2,'targetedSubPASS':3,'cacheControlTopPASS':2,'cacheControlSubPASS':7,'cacheCommandInvocations':1,'cachePackageExecutions':1,'actualKafkaACLTestsDispatched':0,'actualManagedShapeTopsDispatched':0,'actualCacheTopsDispatched':2,'currentCacheTestDeltaScope':receipt,'completedPackageTestDeltaReuse':CACHE_TEST_DELTA_PACKAGE_REUSE,'sameWholeSource':False,'originalGoCommandPassed':False,'originalWholeScopeAccepted':False,'currentWholeOwnedcontrolPackagePASS':False,'currentWholeMsgtransferPackagePASS':False,'currentWholeCachePackagePASS':False,'actualProcessImageTestsDispatched':0,'processImageHistoricalPass':PROCESS_IMAGE_PRIOR_PASS,'releaseControlsHistoricalPass':RELEASE_CONTROLS_PRIOR_PASS,'kafkaConfirmHistoricalPass':KAFKA_CONFIRM_PRIOR_PASS,'kafkaConfirmPassedReuse':None,'kafkaACLPassedReuse':None,'kafkaACLHistoricalPass':KAFKA_ACL_PASSED_REUSE,'cleanupPassedReuse':CLEANUP_PASSED_REUSE,'cleanupRecentReuse':CLEANUP_RECENT_REUSE,'cleanupReuse':dict(CLEANUP_REUSE,topPASS=0,subPASS=1),'launcherPassedReuse':launcher_reuse,'actualLauncherTestsDispatched':0,'redisParserPassedReuse':redis_parser_reuse,'actualRedisParserTestsDispatched':0,'unaffectedHistoricalBatchesRedispatched':False,'historicalCleanupSourceFactsOnly':True,'originalObligation':{'topCount':5,'subCount':12,'crossSource':True},'originalNewSafetyContract':{'topCount':2,'subCount':4,'crossSource':True},'originalCompleteObligation':{'topCount':7,'subCount':16,'crossSource':True},'currentFreshGo':{'topPASS':4,'subPASS':10,'includesOriginalGraph':True},'reuseOldBinary':False,'deployment':False,'resourceCleanup':'must independently pass always collector','privateLogsUploaded':False})

def check_locked(c):
 if FINAL_SOURCE is None or c.get('state')!='FROZEN_FINAL_SOURCE' or c.get('source')!=FINAL_SOURCE or c.get('sourceReviewAccepted') is not True:raise ValueError('Root final formatted whole-source identity required; prepare-only cannot execute')
 if c['scenarios']!=list(SCENARIOS) or c['roles']!=list(ROLES) or c['existingGreenTestsToRerun']!=[] or c['reuseOldBinary'] is not False:raise ValueError('exact fresh graph scope required')
 if c['resource']!=RESOURCE:raise ValueError('reviewed graph resource contract differs')
 if c.get('targetedKafkaACLControl')!=KAFKA_ACL_CONTROL:raise ValueError('exact original Kafka ACL parent and two new children required')
 if c.get('currentCacheTestDeltaControl') is not None:check_cache_test_delta_plan(c)
 elif c.get('currentGroupRecoveryControl') is not None:check_current_group_recovery_plan(c)
 elif c.get('currentProducerControl') is not None:check_current_producer_plan(c)
 elif c.get('currentManagedShapeControl') is not None:check_current_managed_shape_plan(c)
 elif c.get('currentReleaseControl') is not None:check_current_release_plan(c)
 elif c.get('kafkaConfirmPassedReuse') is not None:check_kafka_confirm_reuse_plan(c)
 elif c.get('kafkaACLTestsToRun')==[]:
  if json.dumps(c.get('kafkaACLPassedReuse'),sort_keys=True,separators=(',',':'))!=json.dumps(KAFKA_ACL_PASSED_REUSE,sort_keys=True,separators=(',',':')) or c.get('processImageTestsToRun',[])!=[] or c.get('currentImageKafkaControl') is not None or c.get('currentKafkaConfirmControl') is not None:raise ValueError('exact Root accepted Kafka ACL reuse without redispatch required')
 elif c.get('currentKafkaConfirmControl') is not None:check_current_kafka_confirm_plan(c)
 else:check_current_image_kafka_plan(c)
 if c.get('cleanupScopes')!=CLEANUP_SCOPES or c.get('cleanupCompleteScopes')!=CLEANUP_COMPLETE_SCOPES or c.get('cleanupRecentReuse')!=CLEANUP_RECENT_REUSE or c.get('cleanupFixtureRepair')!=CLEANUP_FIXTURE_REPAIR or c.get('cleanupReuse')!=CLEANUP_REUSE or 'ownedcontrolReuse' in c or c.get('originalTargets')!=GRAPH_TARGETS or c.get('requiredOriginalSubtests')!=GRAPH_CHILDREN or c.get('cleanupPassedReuse')!=CLEANUP_PASSED_REUSE or c.get('cleanupTestsToRun')!=[]:raise ValueError('exact fresh local1/runtime2/graph2+3 with original3top9sub and unbound1 reuse required')
def validate_entries(bodies,c):
    rows = c['entrypoints']
    if len(rows) != 14 or len({r['role'] for r in rows}) != 14 or len({r['package'] for r in rows}) != 14:
        raise ValueError('fourteen actual distinct main packages required')
    families = Counter(r['lifecycleFamily'] for r in rows)
    if families != Counter({'rpc':10,'api':1,'crontask':1,'msggateway':1,'msgtransfer-managed':1}):
        raise ValueError('actual five lifecycle families required')
    if [r['package'] for r in rows] != c['compileOnlyPackages']:
        raise ValueError('compile list is not actual fourteen mains')
    for row in rows:
        raw = bodies.get(row['target']); identity = row['actualMain']
        if (raw is None or row['target'] != 'server/'+row['package'].removeprefix('./')+'/main.go'
                or row['package'].startswith('./cmd/') is not True
                or len(raw) != identity['bytes'] or sha(raw) != identity['sha256']
                or row['outputName'] != row['role'] or not re.fullmatch('[a-z][a-z0-9-]*',row['outputName'])):
            raise ValueError('actual main identity or owned binary output differs')
    for family in c['lifecycleFamilies']:
        for key in ('command','ownedOwner'):
            binding=family[key];raw=bodies.get(binding['target']);identity=binding['source']
            if raw is None or len(raw)!=identity['bytes'] or sha(raw)!=identity['sha256']:
                raise ValueError('actual lifecycle dispatch/owner source differs')
    group = next(r for r in rows if r['role'] == 'rpc-group')
    if b'StartSvrManaged' not in bodies[group['target']]:
        raise ValueError('actual managed group entry cannot be replaced by old main')

def verify():
 c=json.loads(regular(INPUTS));check_locked(c)
 check_launcher_reuse(c.get('launcherPassedReuse'))
 if c.get('launcherTestsToRun')!=[]:raise ValueError('unchanged launcher must use exact historical receipt without Go dispatch')
 for row in c['dependencies']:pin(row)
 parent=module(c['sourceProvider'],'fullgraph_actual_source_parent');old,bodies,recipe=parent.verify()
 if old['source']!=c['parent']:raise ValueError('explicit parent identity differs')
 parent_bodies=dict(bodies);bodies=dict(bodies);seen=set()
 for r in c['overlay']:
  target=r['target'];preimage=bodies.get(target)
  if target in seen or r['path']!=SOURCE_ROOT+target or r['beforeSHA256']!=(sha(preimage) if preimage is not None else None):raise ValueError('exact final overlay preimage required')
  seen.add(target);bodies[target]=pin(r)
 actual={str(p.relative_to(REPO)) for p in (REPO/SOURCE_ROOT).rglob('*') if p.is_file() or p.is_symlink()}
 if seen!=set(c['allowedOverlayTargets']) or actual!={r['path'] for r in c['overlay']} or len(bodies)!=FINAL_SOURCE['members'] or recipe.digest(recipe.rows(bodies))!=FINAL_SOURCE['digest']:raise ValueError('whole final source vector differs')
 for t in ('server/go.mod','server/go.sum','server/config/config.example.yaml'):
  if bodies[t]!=parent_bodies[t]:raise ValueError('original app module locks/config changed')
 if not REQUIRED_GRAPH_SOURCE<=set(bodies):raise ValueError('actual graph and original oracle implementation required')
 if set(c['sourceModes'])!=set(bodies) or any(type(v) is not int or v not in (0o644,0o755) for v in c['sourceModes'].values()):raise ValueError('whole final source mode vector required')
 validate_entries(bodies,c['entrypointContract'])
 catalog=json.loads(pin(c['catalog']))
 for row in c['controllerPins']:pin(row)
 check_redis_parser_reuse(c.get('redisParserPassedReuse'),bodies)
 if c.get('redisParserTestsToRun')!=[]:raise ValueError('unchanged Redis parser must not be redispatched')
 if c.get('currentCacheTestDeltaControl') is not None:
  check_cache_test_delta_plan(c);check_cache_test_delta_reuse(c['cacheTestDeltaPackageReuse'],bodies,recipe);check_process_image_prior(c.get('processImageHistoricalPass'),bodies)
 elif c.get('currentGroupRecoveryControl') is not None:
  check_current_group_recovery_plan(c);check_process_image_prior(c.get('processImageHistoricalPass'),bodies)
 elif c.get('currentProducerControl') is not None:
  check_current_producer_plan(c);check_process_image_prior(c.get('processImageHistoricalPass'),bodies)
 elif c.get('currentManagedShapeControl') is not None:
  check_current_managed_shape_plan(c);check_process_image_prior(c.get('processImageHistoricalPass'),bodies)
 elif c.get('currentReleaseControl') is not None:
  check_current_release_plan(c);check_process_image_prior(c.get('processImageHistoricalPass'),bodies)
 elif c.get('kafkaConfirmPassedReuse') is not None:
  check_kafka_confirm_reuse(c['kafkaConfirmPassedReuse'],bodies,recipe);check_process_image_prior(c.get('processImageHistoricalPass'),bodies)
 elif c['kafkaACLTestsToRun']==[]:check_kafka_acl_reuse(c.get('kafkaACLPassedReuse'),bodies,recipe)
 elif c.get('currentKafkaConfirmControl') is not None:
  check_current_kafka_confirm_plan(c);check_process_image_prior(c.get('processImageHistoricalPass'),bodies)
 else:check_current_image_kafka_plan(c)
 if catalog['scope']!='fresh_owned_fullgraph_compatibility_fixture_only_no_production_default' or catalog['minio']['mode']!='source-elf' or catalog['minio']['status']!='historical_unmaintained_test_only':raise ValueError('explicit source-MinIO fixture required')
 if catalog['images']['minio']!=catalog['images']['redis'] or catalog['images']['keeper']!=catalog['images']['redis']:raise ValueError('known Redis base must remain explicit')
 module('ci/backend-fullgraph/minio_build.py','fullgraph_fixed_fixture_policy').fixture_policy(c)
 return c,bodies,recipe,parent,old,catalog

def go_test_projection_phase(label):
 return label in {'pending-two-original-oracles','launcher-ownedcontrol','redis-parser','kafka-acl-response',CURRENT_IMAGE_KAFKA_CONTROL['phase'],CURRENT_KAFKA_CONFIRM_CONTROL['phase'],CURRENT_RELEASE_CONTROL['phase'],CURRENT_MANAGED_SHAPE_CONTROL['phase'],CURRENT_PRODUCER_CONTROL['phase'],CURRENT_GROUP_RECOVERY_CONTROL['phase'],CURRENT_CACHE_TEST_DELTA_CONTROL['phase']} or label in {scope['label'] for scope in CLEANUP_SCOPES}

def persist_go_test_projection(out,stdout_path,label,command_index):
 if not go_test_projection_phase(label):return False
 raw_events=regular(stdout_path,32<<20)
 if label=='cleanup-runtimeacceptance':
  fixture_observations=runtime_cleanup_observations(raw_events,label)
  write(out/'runtime-cleanup-observations.json',dict(fixture_observations,source=FINAL_SOURCE))
 events,diagnostic=project_go_test_events(raw_events,label)
 if diagnostic is not None:write(out/(f'go-test-compile-diagnostic-{command_index:04d}.json'),diagnostic)
 write(out/('oracle-events.json' if label=='pending-two-original-oracles' else label+'-events.json'),events)
 return True

def project_go_test_events(raw,phase):
 if not go_test_projection_phase(phase):raise ValueError('only exact original and cleanup Go test phases may expose build diagnostics')
 events=[];digest=hashlib.sha256();total=0;head=bytearray()
 for line in raw.splitlines():
  event=json.loads(line)
  events.append({k:event[k] for k in ('Time','Action','Package','Test','Elapsed') if k in event})
  if event.get('Action')=='build-output' and event.get('Test') in (None,''):
   output=event.get('Output')
   if not isinstance(output,str):raise ValueError('public Go build-output must be a string')
   block=output.encode('utf-8');digest.update(block);total+=len(block)
   head.extend(block[:max(0,65536-len(head))])
 if len(events)>4096:raise ValueError('finite original oracle event bound')
 diagnostic=None
 if total:
  text=bytes(head).decode('utf-8','ignore')
  diagnostic={'phase':phase,'publicSourceOnly':True,'selection':'go-test-build-output-without-Test-only','completeBytes':total,'completeSHA256':digest.hexdigest(),'diagnostic':text,'truncated':len(text.encode('utf-8'))<total}
 return events,diagnostic

def runtime_cleanup_observations(raw,phase):
 if phase!='cleanup-runtimeacceptance':raise ValueError('only exact runtime cleanup fixture observations are public')
 marker='LUOBO_RUNTIME_CLEANUP_DIAG_V1 ';top='TestFullgraphEarlyFailureKeepsOriginalChildrenForFinalCleanup';package='github.com/openimsdk/open-im-server/v3/internal/runtimeacceptance'
 kinds={'cancel-audit-before-stop','timeout-before-local-wait-complete'};stages={'begin','private-directory','launcher-open','executable-path','executable-canonical','executable-read','child-directory','bootstrap-write','receipt-write','child-start','child-ready','fixture-ready','assert-early-incomplete','short-cleanup','assert-short-incomplete','final-cleanup-start','signal-wait','release-write','final-cleanup-return','assert-primary','assert-complete','count-check','repeat-cleanup','complete'}
 fields={'version','kind','stage','errorClass','shortErrorClass','role','binaryBytes','childReturned','finalReturned','observedExits','complete','launcherClosed','signal0','signal1','primaryPreserved','elapsedMicros','firstStopMicros','finalCleanupMicros'}
 classes={'none','deadline','cancelled','owned-evidence','not-exist','permission','other'};signals={'unobserved','seen','one','other','unreadable'};rows={};rejected=0
 def unique(pairs):
  result=dict(pairs)
  if len(result)!=len(pairs):raise ValueError('duplicate diagnostic field')
  return result
 for line in raw.splitlines():
  try:event=json.loads(line)
  except (ValueError,TypeError):rejected+=1;continue
  if not isinstance(event,dict) or event.get('Action')!='output' or event.get('Package')!=package or not isinstance(event.get('Test'),str) or event.get('Test') not in {top+'/'+kind for kind in kinds}:continue
  output=event.get('Output')
  if not isinstance(output,str) or not output.startswith(marker):continue
  try:
   if len(output.encode('utf-8'))>2048 or not output.endswith('\n') or output.count('\n')!=1:raise ValueError('bounded single diagnostic line required')
   value=json.loads(output[len(marker):-1],object_pairs_hook=unique)
   if not isinstance(value,dict) or set(value)!=fields or value['version']!='runtime-cleanup-fixture-observation-v1' or value['kind'] not in kinds or event['Test']!=top+'/'+value['kind'] or value['stage'] not in stages or value['errorClass'] not in classes or value['shortErrorClass'] not in classes|{'unobserved'} or value['role'] not in {'none','rpc-msg','msgtransfer-legacy'} or value['signal0'] not in signals or value['signal1'] not in signals:raise ValueError('closed diagnostic enums required')
   if any(type(value[key]) is not bool for key in ('childReturned','finalReturned','complete','launcherClosed','primaryPreserved')):raise ValueError('closed diagnostic booleans required')
   for key,maximum in [('binaryBytes',1<<30),('observedExits',2),('elapsedMicros',180000000),('firstStopMicros',180000000),('finalCleanupMicros',180000000)]:
    if type(value[key]) is not int or not 0<=value[key]<=maximum:raise ValueError('bounded diagnostic integers required')
   if value['kind'] in rows:raise ValueError('one diagnostic per exact failed child required')
  except (ValueError,TypeError,KeyError,UnicodeError):rejected+=1;continue
  rows[value['kind']]=value
 return {'version':'runtime-cleanup-fixture-observations-v1','diagnosticsComplete':set(rows)==kinds and rejected==0,'rejectedMarkers':min(rejected,4096),'observations':[rows[key] for key in sorted(rows)]}

def check_redis_parser_reuse(value,bodies):
 # This exact 122c receipt is historical1/3, never a fresh-source PASS. JSON
 # comparison also keeps numeric/boolean types and the complete four names.
 if not isinstance(value,dict) or json.dumps(value,sort_keys=True,separators=(',',':'))!=json.dumps(REDIS_PARSER_PASSED_REUSE,sort_keys=True,separators=(',',':')):
  raise ValueError('exact Root accepted historical Redis parser receipt required')
 for row in value['sourcePins']:
  raw=bodies.get(row['target'])
  if raw is None or len(raw)!=row['bytes'] or sha(raw)!=row['sha256']:
   raise ValueError('reused Redis parser production/test source changed')
 return value

def load_redis_parser_reuse(out,bodies):
 receipt=check_redis_parser_reuse(dict(REDIS_PARSER_PASSED_REUSE),bodies)
 write(out/'redis-parser-passed-reuse.json',receipt)
 return receipt

def run_redis_parser_control(go,environment,command,parent,out):
 # This pure parser test needs no plane, DB fixture or product process. Its
 # original Go reporter is audited before any success receipt is written.
 control=REDIS_PARSER_CONTROL;targets=control['targets'];package=next(iter(targets));top=targets[package][0]
 raw=command(control['phase'],[go,'test','-mod=readonly','-race','-p=1','-parallel=1','-count=1','-json','-vet=off','-timeout=180s','-run','^'+top+'$','./'+package],control['phaseGuardSeconds'],environment)
 events=[json.loads(line) for line in raw.decode().splitlines() if line.strip()]
 result=parent.audit(events,0,targets)
 expected={(package,top)}|{(package,top+'/'+child) for child in control['requiredSubtests'][package][top]}
 names=[(row['package'],row['name']) for row in result['namedCompletions']]
 if result['topPASS']!=1 or result['subPASS']!=3 or len(names)!=4 or set(names)!=expected:
  raise ValueError('exact Redis parser parent and all three children required')
 receipt=dict(result,source=FINAL_SOURCE,accepted=True,newDatabaseFixtures=0,otherR8TargetsDispatched=False)
 write(out/'redis-parser-names.json',receipt)
 return receipt

def check_kafka_acl_receipt(value):
 # Original complete reporter events and post evidence, not a child-only claim.
 if not isinstance(value,dict) or json.dumps(value,sort_keys=True,separators=(',',':'))!=json.dumps(KAFKA_ACL_PASSED_REUSE,sort_keys=True,separators=(',',':')):raise ValueError('exact Root accepted historical Kafka ACL evidence required')
 for name in ('events','names','sourcePost'):
  record=value[name];raw=(json.dumps(record['body'],separators=(',',':'),ensure_ascii=False)+'\n').encode()
  if len(raw)!=record['bytes'] or sha(raw)!=record['sha256']:raise ValueError('original Kafka ACL evidence body differs')
 control=KAFKA_ACL_CONTROL;package=next(iter(control['targets']));top=control['targets'][package][0]
 expected={(package,top)}|{(package,top+'/'+child) for child in control['requiredSubtests'][package][top]}
 names=value['names']['body'];actual=[(r['package'],r['name']) for r in names['namedCompletions']]
 if names['source']!=value['source'] or names['accepted'] is not True or names['topPASS']!=1 or names['subPASS']!=2 or names['skip']!=0 or names['fail']!=0 or len(actual)!=3 or set(actual)!=expected:raise ValueError('complete original Kafka ACL parent and both children required')
 events=value['events']['body'];prefix='github.com/openimsdk/open-im-server/v3/'
 if any(r.get('Action') in ('fail','skip') for r in events) or any(r.get('Package')!=prefix+package for r in events):raise ValueError('original Kafka ACL event package or result differs')
 for action in ('run','pass'):
  actual=[(package,r['Test']) for r in events if r.get('Test') and r.get('Action')==action]
  if len(actual)!=3 or set(actual)!=expected:raise ValueError('every original Kafka ACL run/pass event required')
 if len([r for r in events if r.get('Action')=='pass' and not r.get('Test')])!=1:raise ValueError('original Kafka ACL package completion required')
 if value['sourcePost']['body']!={'accepted':True,'source':value['source']} or value['toolsBefore']!=value['toolsAfter']:raise ValueError('original source and tool post required')
 return value

def check_kafka_acl_go_tool(value,current_go):
 if not isinstance(current_go,dict) or json.dumps({k:current_go.get(k) for k in ('bytes','sha256','mode')},sort_keys=True,separators=(',',':'))!=json.dumps(value['goTool'],sort_keys=True,separators=(',',':')):raise ValueError('same original Kafka ACL Go executable required')

def check_kafka_acl_reuse(value,bodies,recipe):
 receipt=check_kafka_acl_receipt(value)
 if FINAL_SOURCE!=receipt['source'] or len(bodies)!=receipt['source']['members'] or recipe.digest(recipe.rows(bodies))!=receipt['source']['digest']:raise ValueError('complete unchanged Kafka ACL whole source required')
 return receipt

def load_kafka_acl_reuse(out,bodies,recipe,current_go):
 receipt=check_kafka_acl_reuse(dict(KAFKA_ACL_PASSED_REUSE),bodies,recipe)
 check_kafka_acl_go_tool(receipt,current_go)
 write(out/'kafka-acl-passed-reuse.json',receipt)
 return receipt

def run_kafka_acl_control(go,environment,command,parent,out):
 control=KAFKA_ACL_CONTROL;targets=control['targets'];package=next(iter(targets));top=targets[package][0]
 raw=command(control['phase'],[go,'test','-mod=readonly','-race','-p=1','-parallel=1','-count=1','-json','-vet=off','-timeout=180s','-run','^'+top+'$','./'+package],control['phaseGuardSeconds'],environment)
 events=[json.loads(line) for line in raw.decode().splitlines() if line.strip()]
 result=parent.audit(events,0,targets)
 expected={(package,top)}|{(package,top+'/'+child) for child in control['requiredSubtests'][package][top]}
 names=[(row['package'],row['name']) for row in result['namedCompletions']]
 if result['topPASS']!=1 or result['subPASS']!=2 or len(names)!=3 or set(names)!=expected:raise ValueError('exact Kafka ACL parent and both response children required')
 receipt=dict(result,source=FINAL_SOURCE,accepted=True,newDatabaseFixtures=0,otherKafkaOrRedisTestsDispatched=False)
 write(out/'kafka-acl-response-names.json',receipt)
 return receipt

def run_cleanup_controls(go,environment,command,parent,out):
 results=[]
 for scope in CLEANUP_SCOPES:
  names=sorted(name for tops in scope['targets'].values() for name in tops)
  children=sorted(child for tops in scope['requiredSubtests'].values() for subs in tops.values() for child in subs)
  pattern='^('+'|'.join(re.escape(name) for name in names)+')$/^('+'|'.join(re.escape(child) for child in children)+')$'
  raw=command(scope['label'],[go,'test','-mod=readonly','-race','-p=1','-parallel=1','-count=1','-json','-vet=off','-timeout=180s','-run',pattern]+['./'+pkg for pkg in scope['targets']],240,environment)
  events=[json.loads(line) for line in raw.decode().splitlines() if line.strip()]
  result=parent.audit(events,0,scope['targets'])
  actual={(row['package'],row['name']) for row in result['namedCompletions'] if '/' in row['name']}
  expected={(pkg,top+'/'+child) for pkg,tops in scope['requiredSubtests'].items() for top,subs in tops.items() for child in subs}
  if actual!=expected:raise ValueError('all actual cleanup descendants required')
  write(out/(scope['label']+'-names.json'),result);results.append(result)
 union={'topPASS':sum(row['topPASS'] for row in results),'subPASS':sum(row['subPASS'] for row in results),'namedCompletions':[name for row in results for name in row['namedCompletions']]}
 if (union['topPASS'],union['subPASS'])!=(2,3):raise ValueError('exact fresh cleanup local1/runtime1top2sub required')
 write(out/'cleanup-names.json',union)
 return union

def audit_target_union(original,cleanup):
 entries=original['namedCompletions']+cleanup['namedCompletions'];actual=Counter((row['package'],row['name']) for row in entries)
 graph={(pkg,top) for pkg,tops in GRAPH_TARGETS.items() for top in tops}
 graph|={(pkg,top+'/'+child) for pkg,tops in GRAPH_TARGETS.items() for top in tops for child in GRAPH_CHILDREN[top]}
 def scope_names(scopes):
  names=set()
  for scope in scopes:
   names|={(pkg,top) for pkg,tops in scope['targets'].items() for top in tops}
   names|={(pkg,top+'/'+child) for pkg,tops in scope['requiredSubtests'].items() for top,subs in tops.items() for child in subs}
  return names
 expected=graph|scope_names(CLEANUP_SCOPES)
 if set(actual)!=expected or any(count!=1 for count in actual.values()) or len(expected)!=10:raise ValueError('exact fresh4top6sub, including failed local-first only, required')
 full=graph|scope_names(CLEANUP_COMPLETE_SCOPES)
 recent=Counter((row['package'],row['name']) for row in CLEANUP_RECENT_REUSE['namedCompletions'])
 if set(recent)!=full-expected or any(count!=1 for count in recent.values()) or len(recent)!=12 or (sum('/' not in name for pkg,name in recent),sum('/' in name for pkg,name in recent))!=(3,9):raise ValueError('only actual original3top9sub/partial child reuse required')
 if (CLEANUP_REUSE['package'],'TestOwnedCleanupSharesSingleSignalAcrossOriginalStopPaths') in recent:raise ValueError('failed original parent cannot be supplied by partial child PASS')
 prior={(CLEANUP_REUSE['package'],top+'/'+child) for top,subs in CLEANUP_REUSE['subtests'].items() for child in subs}
 all_names=expected|set(recent)|prior
 new_tops={'TestOwnedProcessImageStreamingKeepsOriginalIdentityAndCancellation','TestOwnedLocalCleanupCancelledValidationAndOriginalDoneStayLocal'}
 new_names={(pkg,name) for pkg,name in full if name.split('/')[0] in new_tops}
 if len(prior)!=1 or full&prior or len(full)!=22 or len(new_names)!=6 or len(all_names-new_names)!=17 or len(all_names)!=23:raise ValueError('complete original5top12sub and new2top4sub obligations cannot shrink')
 receipt=dict(CLEANUP_REUSE,topPASS=0,subPASS=1,namedCompletions=[{'package':pkg,'name':name} for pkg,name in sorted(prior)])
 return {'source':FINAL_SOURCE,'topPASS':4,'subPASS':6,'skip':0,'fail':0,'namedCompletions':[{'package':pkg,'name':name} for pkg,name in sorted(actual)],'cleanupReuse':receipt,'cleanupRecentReuse':CLEANUP_RECENT_REUSE,'originalObligation':{'topCount':5,'subCount':12,'crossSource':True},'newSafety':{'topCount':2,'subCount':4,'crossSource':True},'completeObligation':{'topCount':7,'subCount':16,'crossSource':True}}

def check_launcher_names(value):
 expected=[{'package':'internal/namespaceimport/ownedcontrol','name':'TestOwnedLauncherHoldPersistsAndCannotPrepareMetadata'}]
 if not isinstance(value,dict) or set(value)!={'source','topPASS','subPASS','skip','fail','namedCompletions'} or value.get('source')!=FINAL_SOURCE or value.get('namedCompletions')!=expected:raise ValueError('exact current-source launcher completion required')
 if any(type(value.get(key)) is not int or value[key]!=count for key,count in [('topPASS',1),('subPASS',0),('skip',0),('fail',0)]):raise ValueError('launcher exact1top0sub without fail or skip required')
 return value

def run_launcher_control(go,environment,command,parent,out):
 targets={'internal/namespaceimport/ownedcontrol':['TestOwnedLauncherHoldPersistsAndCannotPrepareMetadata']}
 pattern='^TestOwnedLauncherHoldPersistsAndCannotPrepareMetadata$'
 raw=command('launcher-ownedcontrol',[go,'test','-mod=readonly','-race','-p=1','-parallel=1','-count=1','-json','-vet=off','-timeout=180s','-run',pattern,'./internal/namespaceimport/ownedcontrol'],240,environment)
 events=[json.loads(line) for line in raw.decode().splitlines() if line.strip()]
 actual=parent.audit(events,0,targets)
 receipt=check_launcher_names(dict(source=FINAL_SOURCE,**{key:actual[key] for key in ('topPASS','subPASS','skip','fail','namedCompletions')}))
 write(out/'launcher-names.json',receipt)
 return receipt

def check_launcher_reuse(value):
 # Compare the closed original receipt, including JSON numeric/boolean types.
 # Its source is868e, never FINAL_SOURCE. It is not new-source Go acceptance.
 if not isinstance(value,dict) or json.dumps(value,sort_keys=True,separators=(',',':'))!=json.dumps(LAUNCHER_PASSED_REUSE,sort_keys=True,separators=(',',':')):raise ValueError('exact Root accepted historical launcher receipt required')
 return value

def load_launcher_reuse(out):
 # Source-bound 868e launcher evidence is reused without a new Go dispatch.
 # Its exact Hold/reopen/reject-Start test path does not call the changed
 # bootstrap, Merkle proof, Agent.Acquire, flow or acceptance observation code.
 # Those source changes are not a claim that all dependency bytes are identical.
 receipt=dict(LAUNCHER_PASSED_REUSE)
 write(out/'launcher-passed-reuse.json',check_launcher_reuse(receipt))
 return receipt

def load_cleanup_reuse(out):
 # Root accepted actual2top3sub at575; observer-only bootstrap additions do
 # not enter these unchanged cleanup bodies. Never dispatch Go here.
 receipt=dict(CLEANUP_PASSED_REUSE)
 write(out/'cleanup-passed-reuse.json',receipt)
 return receipt

def audit_current_oracles(original,reused):
 if reused!=CLEANUP_PASSED_REUSE:raise ValueError('exact Root accepted cleanup receipt required')
 # Retain the old closed obligation audit as set arithmetic; its historical
 # helpers/controls remain unchanged. Only new oracle names are fresh here.
 closure=audit_target_union(original,reused)
 return dict(closure,topPASS=2,subPASS=3,namedCompletions=original['namedCompletions'],cleanupPassedReuse=reused,actualCleanupTestsDispatched=0)

def public_cleanup(row):
 if row['version']!='owned-fullgraph-resource-cleanup-v1' or type(row['accepted']) is not bool or not re.fullmatch('[0-9a-f]{32}',row['nonce']) or any(not re.fullmatch('[0-9a-f]{64}',v) for v in row['exactContainerIDs']):raise ValueError('exact resource cleanup receipt required')
 return {'version':row['version'],'ownerNonceSHA256':sha(row['nonce'].encode()),'exactContainerIDs':row['exactContainerIDs'],'unconfirmedIDs':row['unconfirmedIDs'],'accepted':row['accepted']}

def check_business_permissions(value):
 if not isinstance(value,dict) or set(value)!={'version','principals'} or value.get('version')!='owned-fullgraph-business-permissions-v1':raise ValueError('actual finite business permission evidence required')
 rows=value['principals']
 fields={'role','redisWhoami','redisClientInfo','redisAclGetUserError','redisConfigGetError','redisForeignKeyError','kafkaOriginalTopics','kafkaAclDescribeError','kafkaForeignTopicError','kafkaConnectionClosed'}
 if not isinstance(rows,list) or len(rows)!=2:raise ValueError('both original business principals required')
 finite=[]
 for role,row in zip(('legacy','runtime'),rows):
  if not isinstance(row,dict) or set(row)!=fields or row.get('role')!=role:raise ValueError('exact business principal evidence required')
  if any(row.get(k) is not True for k in ('redisWhoami','redisClientInfo','kafkaOriginalTopics','kafkaConnectionClosed')):raise ValueError('actual original identity/topics and connection close required')
  if any(row.get(k)!='NOPERM' for k in ('redisAclGetUserError','redisConfigGetError','redisForeignKeyError')):raise ValueError('typed Redis NOPERM required; absence or transport errors never pass')
  if type(row.get('kafkaAclDescribeError')) is not int or row['kafkaAclDescribeError']!=31 or type(row.get('kafkaForeignTopicError')) is not int or row['kafkaForeignTopicError']!=29:raise ValueError('typed Kafka cluster/topic authorization refusal required')
  finite.append({key:row[key] for key in sorted(fields)})
 return {'version':'owned-fullgraph-business-permissions-v1','principals':finite}

def check_cancel(value,scenario,source,builds,plane_sha):
 permissions=check_business_permissions(value.get("businessPermissions"))
 # The concrete original operator must actually cancel its retained group.
 # Exact cancellation fields are frozen with the final oracle source, never
 # inferred from a non-serving snapshot or from container disappearance.
 if value.get('version')!='owned-fullgraph-token-v2-result-v1' or value.get('scenario')!=scenario or value.get('accepted') is not True or value.get('sourceSHA256')!=source or value.get('resourcePlaneSHA256')!=plane_sha or value.get('phase')!='actual-prerelease-cancel-and-original-joins-accepted' or value.get('freshPlaneOnly') is not True or value.get('knownAdmissionSHA256','') or value.get('actualTokenRPCDispatched')!=0:raise ValueError('actual prerelease cancellation required')
 phase='registered-not-serving-before-readiness' if scenario=='reader-registered-cancel' else 'known-readiness-still-keeps-writer-fence'
 expected={'rpc-msg','rpc-conversation'} if scenario=='reader-registered-cancel' else set(ROLES)
 cancellation=value.get('cancellation')
 if not isinstance(cancellation,dict) or cancellation.get('pausedPhase')!=phase or cancellation.get('actualPreparedRoles')!=len(expected) or value.get('actualStartedRoles')!=0 or value.get('roles') not in (None,[]) or value.get('readerServed') not in (None,[]):raise ValueError('original prerelease roles/fence differ')
 if any(cancellation.get(k) is not True for k in ('knownAdmissionAbsent','readerDiscoveryAbsentBefore','readerDiscoveryAbsentAfter','originalStopReturnedError')):raise ValueError('actual original cancellation refusal required')
 if scenario=='reader-registered-cancel' and value.get('dataReadySHA256','') or scenario=='reader-writer-fence-cancel' and not re.fullmatch('[0-9a-f]{64}',value.get('dataReadySHA256','')):raise ValueError('exact original readiness boundary differs')
 phases=value['readerPhases']
 names=['registered-not-serving-before-readiness','known-readiness-still-keeps-writer-fence'][:1 if len(expected)==2 else 2]
 if [r['phase'] for r in phases]!=names or any(r['bothActualConnectionsNotServing'] is not True for r in phases):raise ValueError('real registered listener observations required')
 rows=cancellation['roles'];by={b['role']:b for b in builds['binaries']}
 if len(rows)!=len(expected) or {r['role'] for r in rows}!=expected or len({r['pid'] for r in rows})!=len(rows) or len({r['ownerNonceSHA256'] for r in rows})!=len(rows):raise ValueError('all original launched roles must be retained')
 for row in rows:
  if row['sourceSHA256']!=source or row['binarySHA256']!=by[row['role']]['sha256'] or not row['startToken'] or any(row.get(k) is not True for k in ('actualAbsent','announcementAbsent','canonicalDrainAbsent','privateReleaseAbsent','originalWaitObserved')):raise ValueError('actual cancellation join/absence differs')
 return {'scenario':scenario,'accepted':True,'source':source,'originalLaunchedRoles':len(rows),'actualAbsentRoles':len(rows),'noAdmission':True,'noRelease':True,'tokenRPCDispatches':0,'businessPermissions':permissions,'originalResultSHA256':None}

def check_result(value,scenario,source,builds,plane_sha):
 if scenario.startswith('reader-'):return check_cancel(value,scenario,source,builds,plane_sha)
 permissions=check_business_permissions(value.get('businessPermissions'))
 if (value['version']!='owned-fullgraph-token-v2-result-v1' or value['scenario']!=scenario or value['accepted'] is not True or value['sourceSHA256']!=source or value['resourcePlaneSHA256']!=plane_sha or value['actualStartedRoles']!=14 or not value['freshPlaneOnly'] or value['actualTokenRPCDispatched']!=1 or not value['rawPushRejected'] or not value['actualUserReadExact'] or not value['redisKeyPresentBefore'] or not value['redisKeyAbsentAfter'] or value['mongoMessageCount']!=0):raise ValueError('actual complete fullgraph result required')
 rows=value['roles'];by={b['role']:b for b in builds['binaries']}
 if len(rows)!=14 or {r['role'] for r in rows}!=set(ROLES) or len({r['pid'] for r in rows})!=14 or len({r['ownerNonceSHA256'] for r in rows})!=14:raise ValueError('fourteen original distinct process owners required')
 for r in rows:
  if not r['actualAbsent'] or r['sourceSHA256']!=source or r['binarySHA256']!=by[r['role']]['sha256']:raise ValueError('original process/binary/source or cleanup differs')
  expected=not(scenario=='redis-unknown' and r['role']=='push')
  if r['canonicalDrain']!=expected or expected and not re.fullmatch('[0-9a-f]{64}',r.get('drainSHA256','')):raise ValueError('unknown must never become canonical drain')
 phases=value['readerPhases'];names=['registered-not-serving-before-readiness','known-readiness-still-keeps-writer-fence','known-admission-without-private-release-still-not-serving']
 if len(phases)!=3 or [r['phase'] for r in phases]!=names or any(not r['bothActualConnectionsNotServing'] for r in phases):raise ValueError('actual staged reader fences required')
 served=value.get('readerServed')
 if not isinstance(served,list) or len(served)!=2 or {r['role'] for r in served}!={'rpc-msg','rpc-conversation'}:raise ValueError('same actual readers must respond after release')
 original={r['role']:r for r in phases[0]['endpoints']}
 for row in served:
  before=original[row['role']]
  if any(row.get(k)!=before.get(k) for k in ('pid','startToken','endpoint','configurationSHA256','listenerInode')) or any(row.get(k) is not True for k in ('actualTCPConnected','originalListenerRetained','actualUnimplementedAfterRelease')):raise ValueError('release must use same original listener and owner')
 loss=value['ackLoss'];unknown=scenario=='redis-unknown'
 if loss.get('version')!='real-redis-exact-del-ack-loss-v1' or loss.get('exactDeleteRequests')!=int(unknown) or loss.get('actualReplyDiscarded')!=unknown or loss.get('transportFailure') is not False or unknown and loss.get('actualUpstreamIntegerOne') is not True:raise ValueError('exact actual Redis ACK outcome required')
 if value['actualTokenRPCSucceeded']!=(scenario=='ack') or value['canonicalShutdown']!=(scenario=='ack') or value['unknownOwnerRetained']!=(scenario=='redis-unknown'):raise ValueError('known/unknown boundary differs')
 return {'scenario':scenario,'accepted':True,'source':source,'actualStartedRoles':14,'actualAbsentRoles':14,'readerPhases':names,'tokenRPCDispatches':1,'tokenSucceeded':value['actualTokenRPCSucceeded'],'canonicalShutdown':value['canonicalShutdown'],'unknownOwnerRetained':value['unknownOwnerRetained'],'mongoMessageCount':0,'businessPermissions':permissions,'originalResultSHA256':None}

def tool_snapshot():
 names={'python':sys.executable,'go':shutil.which('go'),'gcc':shutil.which('gcc'),'git':'/usr/bin/git','docker':'/usr/bin/docker','sudo':'/usr/bin/sudo','nsenter':'/usr/bin/nsenter','setpriv':'/usr/bin/setpriv','env':'/usr/bin/env'}
 result={}
 for role,path in names.items():
  if path is None:raise ValueError('required hosted tool absent')
  p=Path(path).resolve();raw=regular(p)
  result[role]={'path':str(p),'bytes':len(raw),'sha256':sha(raw),'mode':stat.S_IMODE(p.stat().st_mode)}
 return result

def job_identity():
 return {'version':'owned-fullgraph-ci-allocation-v1','uid':os.getuid(),'runID':os.environ['GITHUB_RUN_ID'],'attempt':os.environ['GITHUB_RUN_ATTEMPT'],'head':os.environ['GITHUB_SHA']}
def allocation_root():
 run,attempt=os.environ['GITHUB_RUN_ID'],os.environ['GITHUB_RUN_ATTEMPT']
 if not run.isdecimal() or not attempt.isdecimal() or not re.fullmatch('[0-9a-f]{40}',os.environ['GITHUB_SHA']):raise ValueError('exact original hosted attempt required')
 return Path(os.environ['RUNNER_TEMP'])/('backend-fullgraph-'+run+'-'+attempt)
def admit():
 c,_,_,_,_,_=verify();root=allocation_root()
 if root.resolve()!=root or root.exists() or os.statvfs(root.parent).f_bavail*os.statvfs(root.parent).f_frsize<c['resource']['initialBytes']:raise ValueError('fresh allocation and cold4GiB admission required')
 root.mkdir(mode=0o700);write(root/'allocation-owner.json',job_identity())
 out=REPO/'ci-results';out.mkdir(exist_ok=True)
 write(out/'backend-fullgraph-admission.json',{'accepted':True,'minimumInitialBytes':c['resource']['initialBytes'],'source':c['source'],'businessDeployment':False})

def source_post(root,bodies,c,template):
 post={'accepted':False,'source':FINAL_SOURCE}
 try:
  for target,raw in bodies.items():
   p=root/target
   if regular(p)!=raw or stat.S_IMODE(p.stat().st_mode)!=c['sourceModes'][target]:raise ValueError('whole source or mode drift')
  if regular(root/'server/config/config.yaml')!=template or stat.S_IMODE((root/'server/config/config.yaml').stat().st_mode)!=0o600:raise ValueError('generated example config drift')
  verify();post['accepted']=True
 except Exception as error:post['exceptionType']=type(error).__name__
 return post

def prepare_resource_plane(plane,scenario):
 primary=None
 try:
  return plane.prepare()
 except BaseException as error:
  primary=error
  raise
 finally:
  # Snapshot only this original synchronous prepare call, before any cleanup.
  # An observation failure must never replace its original prepare exception.
  try:
   observation=plane.prepare_observation()
   if observation is not None:
    write(plane.private/('prepare-observation-'+scenario+'.json'),dict(version='owned-fullgraph-resource-prepare-observation-v1',scenario=scenario,**observation,rawErrorText=False))
  except BaseException:
   if primary is None:raise

def run():
 fixture_token=os.environ.pop('GH_TOKEN',None)  # API-only; never inherited by Go/Docker/operator children.
 if any(os.environ.get(k)!=v for k,v in {'GITHUB_ACTIONS':'true','RUNNER_ENVIRONMENT':'github-hosted','RUNNER_OS':'Linux','GOTOOLCHAIN':'local','GOWORK':'off'}.items()) or os.getuid()==0:raise ValueError('disposable hosted nonroot Linux only')
 c,bodies,recipe,parent,old,catalog=verify();started=time.monotonic();temporary=Path(os.environ['RUNNER_TEMP']);allocation=allocation_root();root=allocation/'work'
 if temporary.resolve()!=temporary or allocation.resolve()!=allocation or root.exists() or os.statvfs(temporary).f_bavail*os.statvfs(temporary).f_frsize<c['resource']['initialBytes']:raise ValueError('fresh owned root and initial floor required')
 if json.loads(regular(allocation/'allocation-owner.json'))!=job_identity():raise ValueError('original allocation admission differs')
 root.mkdir(mode=0o700);private=root/'private';private.mkdir(mode=0o700);out=REPO/'ci-results/backend-fullgraph';out.mkdir(mode=0o700,parents=True,exist_ok=False)
 write(private/'job-owner.json',{'version':'owned-fullgraph-ci-job-v1','uid':os.getuid(),'runID':os.environ['GITHUB_RUN_ID'],'attempt':os.environ['GITHUB_RUN_ATTEMPT'],'head':os.environ['GITHUB_SHA'],'source':FINAL_SOURCE})
 for target,raw in bodies.items():
  p=root/target;p.parent.mkdir(parents=True,exist_ok=True);p.write_bytes(raw);p.chmod(c['sourceModes'][target])
 template=bodies['server/config/config.example.yaml'];(root/'server/config/config.yaml').write_bytes(template);(root/'server/config/config.yaml').chmod(0o600)
 write(private/'source-materialized.json',{'source':FINAL_SOURCE,'accepted':True})
 primary=None
 try:
  tools=tool_snapshot();write(out/'tools-before.json',tools)
  builder=module('ci/backend-fullgraph/minio_build.py','owned_minio_source_builder');resources=module('ci/backend-fullgraph/resources.py','owned_fullgraph_resources');selectors=module('ci/backend-fullgraph/service_selectors.py','owned_fullgraph_selectors')
  command_index=0
  def command(label,argv,timeout,env=None,cwd=None):
   nonlocal command_index
   command_index+=1;remaining=c['resource']['controllerSeconds']-(time.monotonic()-started)
   if remaining<1:raise ValueError('controller budget exhausted')
   timeout=max(1,int(min(timeout,remaining)));folder=private/(f'{command_index:04d}-'+label);folder.mkdir(mode=0o700)
   args=[sys.executable,'-B',str(REPO/'ci/group-effects/capture.py'),'--stdout',str(folder/'stdout'),'--stderr',str(folder/'stderr'),'--']
   if env is not None:args+=['/usr/bin/env','-i']+[k+'='+v for k,v in env.items()]
   args+=argv
   wrapped=[sys.executable,'-B',str(REPO/'ci/group-effects/resource_guard.py'),'--cwd',str(cwd or root/'server'),'--log',str(folder/'guard.log'),'--record',str(folder/'resource.json'),'--min-free-mib','1024','--timeout',str(timeout),'--poll-interval','.2','--allocated-root',str(root),'--max-allocated-mib','16384','--']+args
   child=subprocess.run(wrapped,check=False,timeout=timeout+30);resource=json.loads(regular(folder/'resource.json'))
   failed=child.returncode!=0 or resource.get('status')!='passed' or resource.get('exit_code')!=0
   raw_custody={}
   for name in ('stdout','stderr'):
    try:
     value=regular(folder/name,32<<20);raw_custody[name]={'bytes':len(value),'sha256':sha(value),'private':True}
    except Exception as error:raw_custody[name]={'private':True,'exceptionType':type(error).__name__}
   write(out/(f'phase-{command_index:04d}.json'),{'phase':label,'exitCode':child.returncode,'resource':resource,'rawCustody':raw_custody})
   if failed and (label.startswith('compile-') or label=='minio-source-compile'):
    # These commands run solely on reviewed public source, before any private
    # resource credentials/configs exist. No operator/DB/SDK text is selected.
    raw=regular(folder/'stderr',32<<20) if (folder/'stderr').exists() else b''
    write(out/(f'compile-failure-{command_index:04d}.json'),{'phase':label,'publicSourceOnly':True,'completeBytes':len(raw),'completeSHA256':sha(raw),'diagnostic':raw[:65536].decode('utf-8','replace'),'truncated':len(raw)>65536})
   persist_go_test_projection(out,folder/'stdout',label,command_index)
   if failed:raise ValueError('actual phase failed; private raw retained')
   return regular(folder/'stdout',32<<20)
  env={'PATH':os.environ['PATH'],'HOME':os.environ['HOME'],'GOTOOLCHAIN':'local','GOWORK':'off','GOENV':'off','GOAUTH':'off','GOMAXPROCS':'2','GOMEMLIMIT':'900MiB','CGO_ENABLED':'1'}
  for key,name in [('GOMODCACHE','modcache'),('GOCACHE','buildcache'),('GOPATH','gopath'),('TMPDIR','tmp')]:
   p=root/name;p.mkdir(mode=0o700);env[key]=str(p)
  env['GOTMPDIR']=env['TMPDIR'];results=[]
  env['GOROOT']=command('go-root',['go','env','GOROOT'],15,env).decode().strip();go=str(Path(env['GOROOT'])/'bin/go')
  if command('go-version',[go,'version'],15,env).decode().strip()!='go version go1.27.2 linux/amd64':raise ValueError('exact Go1.27.2 required')
  tool_sha=sha(regular(Path(go).resolve()))
  if tool_sha!=tools['go']['sha256']:raise ValueError('Go command differs from observed original tool')
  reused_fixture=None
  if c['minioFixtureBinding'] is not None:
   reused_fixture=builder.receive(root,catalog,c['minioFixtureBinding'],tool_sha,fixture_token,os.environ['GITHUB_REPOSITORY'])
  fixture_token=None
  command('app-module-download',[go,'mod','download','-json'],600,dict(env,GOPROXY='https://proxy.golang.org',GOSUMDB='sum.golang.org'))
  offline=dict(env,GOPROXY='off',GOSUMDB='off')
  redis_parser_reuse=load_redis_parser_reuse(out,bodies)
  launcher_reuse=load_launcher_reuse(out)
  cleanup_audit=load_cleanup_reuse(out)
  kafka_acl_reuse=None;current_image_kafka=None;current_kafka_confirm=None;kafka_confirm_reuse=None;current_release=None;current_managed_shape=None;current_producer=None;current_group_recovery=None;current_cache_test_delta=None
  if c.get('currentCacheTestDeltaControl') is not None:current_cache_test_delta=run_current_cache_test_delta_control(go,offline,command,parent,out,bodies,recipe,tools['go'])
  elif c.get('currentGroupRecoveryControl') is not None:current_group_recovery=run_current_group_recovery_control(go,offline,command,parent,out)
  elif c.get('currentProducerControl') is not None:current_producer=run_current_producer_control(go,offline,command,parent,out,tools['go'])
  elif c.get('currentManagedShapeControl') is not None:current_managed_shape=run_current_managed_shape_control(go,offline,command,parent,out)
  elif c.get('currentReleaseControl') is not None:current_release=run_current_release_control(go,offline,command,parent,out)
  elif c.get('kafkaConfirmPassedReuse') is not None:kafka_confirm_reuse=load_kafka_confirm_reuse(out,bodies,recipe,tools['go'])
  elif c['kafkaACLTestsToRun']==[]:kafka_acl_reuse=load_kafka_acl_reuse(out,bodies,recipe,tools['go'])
  elif c.get('currentKafkaConfirmControl') is not None:current_kafka_confirm=run_current_kafka_confirm_control(go,offline,command,parent,out)
  else:current_image_kafka=run_current_image_kafka_control(go,offline,command,parent,out)
  binroot=root/'bin';binroot.mkdir(mode=0o700);binaries=[];build_start=time.monotonic()
  entries=[{'role':'acceptance-operator','package':'./cmd/tools/owned-fullgraph-acceptance'}]+c['entrypointContract']['entrypoints']+[{'role':'msgtransfer-legacy','package':'./cmd/openim-msgtransfer'}]
  for entry in entries:
   role=entry['role'];binary=binroot/role;remaining=1200-(time.monotonic()-build_start)
   if remaining<1:raise ValueError('actual14 and auxiliary compile budget exhausted')
   command('compile-'+role,[go,'build','-mod=readonly','-p=1','-o',str(binary),entry['package']],int(remaining),offline)
   raw=builder.stable(binary)
   if raw[:6]!=b'\x7fELF\x02\x01' or len(raw)<20 or raw[18:20]!=b'\x3e\x00':raise ValueError('actual linux amd64 executable required')
   receipt={'schema':1,'sourceSHA256':FINAL_SOURCE['digest'],'binarySHA256':sha(raw),'binaryBytes':len(raw),'toolchainSHA256':tool_sha};receiptpath=binroot/(role+'-receipt.json');receiptsha=write(receiptpath,receipt)
   binaries.append({'role':role,'path':str(binary),'sha256':sha(raw),'sourceSHA256':FINAL_SOURCE['digest'],'receiptPath':str(receiptpath),'receiptSHA256':receiptsha,'services':selectors.SERVICES.get(role,[]),'components':selectors.COMPONENTS.get(role,[])})
   write(out/('compiled-'+role+'.json'),{'role':role,'package':entry['package'],'source':FINAL_SOURCE,'bytes':len(raw),'sha256':sha(raw),'executed':False})
  builds={'version':'source-bound-fullgraph-builds-v1','sourceSHA256':FINAL_SOURCE['digest'],'binaries':[b for b in binaries if b['role'] in ROLES],'legacyMessageTransfer':next(b for b in binaries if b['role']=='msgtransfer-legacy')};buildpath=private/'builds.json';buildsha=write(buildpath,builds);operator=next(b['path'] for b in binaries if b['role']=='acceptance-operator')
  if c['minioFixtureBinding'] is None:
   minio,receipt=builder.build(root,catalog,command,go,tool_sha,env)
   write(out/'source-minio-acquisition.json',{'mode':'original-source-build','fixtureOnly':True,'productRuntimeAccepted':False})
  else:
   minio,receipt=reused_fixture
   write(out/'source-minio-acquisition.json',{'mode':'root-accepted-fixture-artifact','fixtureOnly':True,'productRuntimeAccepted':False,'artifactID':c['minioFixtureBinding']['artifactID'],'rootAcceptanceSHA256':c['minioFixtureBinding']['rootAcceptanceSHA256'],'freshMinioCompile':False})
  write(out/'source-minio-build.json',receipt)
  for image in sorted(set(catalog['images'].values())):command('image-acquire-'+str(len(results))+'-'+sha(image.encode())[:10],['/usr/bin/docker','--host','unix:///var/run/docker.sock','pull','--platform','linux/amd64',image],600)
  for scenario in SCENARIOS:
   if c['resource']['controllerSeconds']-(time.monotonic()-started)<c['resource']['scenarioSeconds']+180:raise ValueError('complete operator-context plus original cleanup budget required before new plane')
   plane=None;scenario_error=None
   try:
    plane=resources.Plane(root/('plane-'+scenario),catalog['images'],minio_source=minio)
    claim=prepare_resource_plane(plane,scenario);plane_path=plane.private/'plane.json';cred=plane.private/'credentials.json';work=root/('scenario-'+scenario);work.mkdir(mode=0o700)
    p={'version':'owned-fullgraph-acceptance-v1','scenario':scenario,'work':str(work),'planePath':str(plane_path),'planeSHA256':sha(regular(plane_path)),'credentialsPath':str(cred),'credentialsSHA256':sha(regular(cred)),'buildsPath':str(buildpath),'buildsSHA256':buildsha};pp=private/(scenario+'-input.json');psha=write(pp,p)
    keeper=next(r for r in claim['observations'] if r['role']=='keeper');groups=','.join(str(g) for g in sorted(set(os.getgroups()+[os.getgid()])))
    args=['sudo','-n','/usr/bin/nsenter','--net=/proc/'+str(keeper['PID'])+'/ns/net','--','/usr/bin/setpriv','--reuid',str(os.getuid()),'--regid',str(os.getgid()),'--groups',groups,'--no-new-privs','--','/usr/bin/env','-i','PATH=/usr/bin:/bin','HOME='+os.environ['HOME'],'TMPDIR='+str(work),operator,'--private-input',str(pp),'--private-input-sha256',psha]
    write(work/'operator-intent.json',{'version':'owned-fullgraph-operator-intent-v1','scenario':scenario,'privateInputSHA256':psha,'operatorSHA256':next(b['sha256'] for b in binaries if b['role']=='acceptance-operator'),'sourceSHA256':FINAL_SOURCE['digest']})
    command('runtime-'+scenario,args,1380)
    result_path=work/'result.json';raw=regular(result_path,1<<20);value=json.loads(raw);safe=check_result(value,scenario,FINAL_SOURCE['digest'],builds,p['planeSHA256']);safe['originalResultSHA256']=sha(raw);write(out/(scenario+'-result.json'),safe);results.append((scenario,result_path,sha(raw)))
   except BaseException as error:scenario_error=error;raise
   finally:
    if plane is not None:
     try:
      cleanup=plane.cleanup()
      write(out/('resource-cleanup-'+scenario+'.json'),public_cleanup(cleanup))
     except Exception:
      if scenario_error is None:raise
  tests_env=dict(offline,LUOBO_FULLGRAPH_SOURCE_SHA256=FINAL_SOURCE['digest'])
  for scenario,path,pin_sha in results:
   prefix='LUOBO_FULLGRAPH_'+scenario.upper().replace('-','_');tests_env[prefix+'_RESULT']=str(path);tests_env[prefix+'_SHA256']=pin_sha
  pattern='^(TestOwnedPreparedReaderRegistersBeforeServeAndRetainsFinalWriterFence|TestOwnedPushActualOwnerClosesOriginalResourcesBeforeRedisUnknownNonpermit)$/^(registered-not-serving-before-readiness|known-readiness-still-keeps-writer-fence|required-token-only)$'
  raw=command('pending-two-original-oracles',[go,'test','-mod=readonly','-race','-p=1','-parallel=1','-count=1','-json','-vet=off','-timeout=180s','-run',pattern,'./pkg/common/startrpc'],240,tests_env)
  events=[json.loads(x) for x in raw.decode().splitlines() if x.strip()];tops={'pkg/common/startrpc':['TestOwnedPreparedReaderRegistersBeforeServeAndRetainsFinalWriterFence','TestOwnedPushActualOwnerClosesOriginalResourcesBeforeRedisUnknownNonpermit']};audit=parent.audit(events,0,tops)
  expected={tops['pkg/common/startrpc'][0]+'/'+s for s in ('registered-not-serving-before-readiness','known-readiness-still-keeps-writer-fence')}|{tops['pkg/common/startrpc'][1]+'/required-token-only'}
  if {r['name'] for r in audit['namedCompletions'] if '/' in r['name']}!=expected:raise ValueError('exact original two tops/three children required')
  write(out/'oracle-names.json',audit)
  targeted_audit=audit_current_oracles(audit,cleanup_audit)
  targeted_audit=dict(targeted_audit,launcherPassedReuse=check_launcher_reuse(launcher_reuse),actualLauncherTestsDispatched=0)
  write(out/'targeted-names.json',targeted_audit)
 except BaseException as error:primary=error;raise
 finally:
  post=source_post(root,bodies,c,template)
  try:
   after=tool_snapshot();write(out/'tools-after.json',after)
   if after!=tools:raise ValueError('hosted tool body/mode changed')
  except Exception as error:post['accepted']=False;post['toolExceptionType']=type(error).__name__
  try:write(out/'source-post.json',post)
  except Exception:
   if primary is None:raise
  if not post['accepted'] and primary is None:raise ValueError('source-post rejected')
 if current_cache_test_delta is not None:
  write_current_cache_test_delta_summary(out,current_cache_test_delta,cleanup_audit,launcher_reuse,redis_parser_reuse)
 elif current_group_recovery is not None:
  write_current_group_recovery_summary(out,current_group_recovery,cleanup_audit,launcher_reuse,redis_parser_reuse)
 elif current_producer is not None:
  write_current_producer_summary(out,current_producer,cleanup_audit,launcher_reuse,redis_parser_reuse)
 elif current_managed_shape is not None:
  write_current_managed_shape_summary(out,current_managed_shape,cleanup_audit,launcher_reuse,redis_parser_reuse)
 elif current_release is not None:
  write_current_release_summary(out,current_release,cleanup_audit,launcher_reuse,redis_parser_reuse)
 elif kafka_confirm_reuse is not None:
  write_kafka_confirm_reuse_summary(out,kafka_confirm_reuse,cleanup_audit,launcher_reuse,redis_parser_reuse)
 elif current_kafka_confirm is not None:
  write_current_kafka_confirm_summary(out,current_kafka_confirm,cleanup_audit,launcher_reuse,redis_parser_reuse)
 elif current_image_kafka is not None:
  write_current_image_kafka_summary(out,current_image_kafka,cleanup_audit,launcher_reuse,redis_parser_reuse)
 else:
  write(out/'summary.json',{'accepted':True,'source':FINAL_SOURCE,'actualCurrentMainBuilds':14,'auxiliaryBuilds':2,'freshGraphScenarios':4,'pendingOriginalTopPASS':2,'pendingOriginalSubPASS':3,'cleanupTopPASS':0,'cleanupSubPASS':0,'cleanupPassedReuse':CLEANUP_PASSED_REUSE,'launcherTopPASS':0,'launcherSubPASS':0,'launcherPassedReuse':LAUNCHER_PASSED_REUSE,'actualLauncherTestsDispatched':0,'targetedTopPASS':2,'targetedSubPASS':3,'kafkaACLTopPASS':0,'kafkaACLSubPASS':0,'kafkaACLHistoricalTopPASS':1,'kafkaACLHistoricalSubPASS':2,'actualKafkaACLTestsDispatched':0,'kafkaACLPassedReuse':kafka_acl_reuse,'redisParserTopPASS':0,'redisParserSubPASS':0,'redisParserHistoricalTopPASS':1,'redisParserHistoricalSubPASS':3,'actualRedisParserTestsDispatched':0,'redisParserPassedReuse':redis_parser_reuse,'cleanupReuse':dict(CLEANUP_REUSE,topPASS=0,subPASS=1),'cleanupRecentReuse':CLEANUP_RECENT_REUSE,'originalObligation':{'topCount':5,'subCount':12,'crossSource':True},'newSafety':{'topCount':2,'subCount':4,'crossSource':True},'completeObligation':{'topCount':7,'subCount':16,'crossSource':True},'old67or21TestsRerun':0,'reuseOldBinary':False,'deployment':False,'resourceCleanup':'must independently pass always collector','privateLogsUploaded':False})
 return 0

if __name__=='__main__':
 p=argparse.ArgumentParser();g=p.add_mutually_exclusive_group();g.add_argument('--verify',action='store_true');g.add_argument('--admit',action='store_true');a=p.parse_args()
 if a.verify:
  c,_,_,_,_,_=verify();print(json.dumps({'source':c['source'],'actualRun':False,'Go':0,'DB':0,'containers':0}))
 elif a.admit:admit()
 else:raise SystemExit(run())
