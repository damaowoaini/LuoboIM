import 'dart:async';
import 'dart:convert';

import 'package:flutter/foundation.dart' show debugPrintSynchronously;
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_openim_sdk/flutter_openim_sdk.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:just_audio/just_audio.dart';
import 'package:imapp/features/chat/presentation/chat_page.dart';
import 'package:imapp/features/chat/presentation/chat_content_access.dart';
import 'package:imapp/features/chat/presentation/chat_merged_messages_page.dart';
import 'package:imapp/features/chat/presentation/message_media.dart';
import 'package:imapp/providers.dart';

import '../../core/im/sdk_harness.dart';
import 'batch_harness.dart';

// Synthetic fixture metadata only: no URL, body, owner, credential or token.
void _phase(String name, [Map<String, Object?> values = const {}]) {
  debugPrintSynchronously(jsonEncode({'mediaPhase': name, ...values}));
}

class _AudioStub extends Fake implements AudioPlayer {
  final events = StreamController<PlayerState>.broadcast(sync: true);
  final entered = Completer<void>();
  Completer<void>? urlRelease;
  int plays = 0, stops = 0, pauses = 0;
  @override
  Stream<PlayerState> get playerStateStream => events.stream;
  @override
  ProcessingState get processingState => ProcessingState.ready;
  @override
  Future<void> stop() async {
    stops++;
    _phase('stop-before-event', {'stops': stops});
    events.add(PlayerState(false, ProcessingState.ready));
    _phase('stop-after-event');
  }

  @override
  Future<void> pause() async {
    pauses++;
    events.add(PlayerState(false, ProcessingState.ready));
  }

  @override
  Future<void> play() async {
    plays++;
    _phase('player-play', {'plays': plays});
    events.add(PlayerState(true, ProcessingState.ready));
  }

  @override
  Future<void> seek(Duration? position, {int? index}) async {}
  @override
  Future<Duration?> setUrl(
    String url, {
    Map<String, String>? headers,
    Duration? initialPosition,
    bool preload = true,
    dynamic tag,
  }) async {
    _phase('set-url-entered');
    if (!entered.isCompleted) entered.complete();
    await urlRelease?.future;
    return const Duration(seconds: 2);
  }

  @override
  Future<void> dispose() async {
    await events.close();
  }
}

void main() {
  late BatchHarness h;
  late _AudioStub player;
  ChatAudioPlayback? playback;
  Map<String, dynamic> merge({
    bool rootPrivate = false,
    bool siblingPrivate = false,
  }) => {
    ...SdkHarness.message(id: 'merge-root', text: ''),
    'contentType': 107,
    'attachedInfoElem': {'isPrivateChat': rootPrivate},
    'mergeElem': {
      'multiMessage': [
        {
          ...SdkHarness.message(id: 'voice-child', text: ''),
          'contentType': 103,
          'soundElem': {
            'sourceUrl': 'https://media.example.com/synthetic.m4a',
            'duration': 2,
          },
        },
        {
          ...SdkHarness.message(id: 'file-child', text: ''),
          'contentType': 105,
          'fileElem': {
            'sourceUrl': 'https://media.example.com/synthetic.txt',
            'fileName': 'nested-file.txt',
            'fileSize': 4,
          },
        },
        if (siblingPrivate)
          {
            ...SdkHarness.message(id: 'private-sibling', text: 'inline-only'),
            'attachedInfoElem': {'isPrivateChat': true},
          },
      ],
    },
  };
  setUp(() {
    h = BatchHarness();
    player = _AudioStub();
    playback = null;
  });
  Future<void> mount(WidgetTester tester, Map<String, dynamic> wire) async {
    h.sdk.messages
      ..clear()
      ..add(wire);
    _phase('sdk-start');
    await tester.runAsync(h.start);
    _phase('sdk-ready');
    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          imGatewayProvider.overrideWithValue(h.sdk.gateway),
          appRuntimeProvider.overrideWith((_) => h.sdk.runtime),
        ],
        child: MaterialApp(
          home: ChatPage(
            conversationId: 'single-alice-bob',
            playbackFactory: (failure) => playback = ChatAudioPlayback(
              onFailure: failure,
              playerFactory: () => player,
            ),
          ),
        ),
      ),
    );
    _phase('widget-mounted');
    await tester.pumpAndSettle();
    _phase('widget-settled');
    await tester.tap(find.textContaining('聊天记录 ·'));
    _phase('viewer-tapped');
    await tester.pumpAndSettle();
    _phase('viewer-settled');
  }

  Future<void> close(WidgetTester tester) async {
    await tester.runAsync(h.dispose);
    await tester.pumpWidget(const SizedBox.shrink());
  }

  for (final action in ['revoke', 'expiry', 'clear', 'exit']) {
    for (final pendingUrl in [false, true]) {
      testWidgets(
        'nested audio ${pendingUrl ? 'late setUrl cannot start' : 'active playback stops'} after root $action',
        (tester) async {
          if (pendingUrl) player.urlRelease = Completer<void>();
          await mount(tester, merge());
          try {
            final viewer = tester.widget<ChatMergedMessagesPage>(
              find.byType(ChatMergedMessagesPage),
            );
            final voice = viewer.messages.singleWhere(
              (item) => item.id == 'voice-child',
            );
            final viewerRoute = ModalRoute.of(
              tester.element(find.byType(ChatMergedMessagesPage)),
            )!;
            bool routeCurrent() => viewerRoute.isCurrent;
            final access = ChatContentAccess(
              repository: h.source,
              root: viewer.root,
              child: voice,
              isRouteCurrent: routeCurrent,
            );
            Map<String, Object?> diagnostics() => {
              'lifecycle': tester.binding.lifecycleState?.name ?? 'null',
              'foreground':
                  tester.binding.lifecycleState == null ||
                  tester.binding.lifecycleState == AppLifecycleState.resumed,
              'routeCurrent': routeCurrent(),
              'ownerCurrent': h.source.current,
              'rootView': h.source.canView(viewer.root),
              'childView': h.source.canView(voice),
              'jointView': access.privacy.canViewAt(DateTime.now()),
              'jointLeaseView': access.canView,
              'httpsPlayable': voice.playableMedia?.scheme == 'https',
              'stops': player.stops,
              'plays': player.plays,
              'messageBound': playback?.messageId == 'voice-child',
              'onFailureNotice': find.text('语音播放失败，请重试').evaluate().isNotEmpty,
              'pauses': player.pauses,
              'unavailableNotice': find.text('消息内容已不可用').evaluate().isNotEmpty,
            };
            _phase('mounted', diagnostics());
            expect(access.canView, true, reason: diagnostics().toString());
            _phase('access-asserted', diagnostics());
            expect(voice.playableMedia?.scheme, 'https');
            await tester.tap(find.text('语音消息').hitTestable());
            _phase('tapped', diagnostics());
            await tester.pump();
            _phase('pumped', diagnostics());
            await tester.runAsync(
              () => player.entered.future.timeout(
                const Duration(seconds: 2),
                onTimeout: () => throw TestFailure(
                  'Audio setUrl was not reached: ${diagnostics()}',
                ),
              ),
            );
            _phase('entered', diagnostics());
            if (!pendingUrl) {
              await tester.pump();
              expect(player.plays, 1);
            }
            if (action == 'revoke') {
              _phase('revoke-start', diagnostics());
              await tester.runAsync(
                () => h.sdk
                    .callback(ListenerType.advancedMsgListener, {
                      'type': 'onNewRecvMessageRevoked',
                      'data': {
                        'messageRevoked': jsonEncode({
                          'clientMsgID': 'merge-root',
                        }),
                      },
                    })
                    .timeout(const Duration(seconds: 2)),
              );
              _phase('revoke-complete', diagnostics());
            } else if (action == 'expiry') {
              h.sdk.gateway.privacyLedger.observe('single-alice-bob', {
                'clientMsgID': 'merge-root',
                'attachedInfoElem': {
                  'isPrivateChat': true,
                  'hasReadTime': 1700000000000,
                  'burnDuration': 30,
                },
              }, now: DateTime.now());
            } else if (action == 'clear') {
              await tester.runAsync(
                () => h.sdk.gateway.clearConversationMessages(
                  h.sdk.session.state.generation,
                  'single-alice-bob',
                ),
              );
            } else {
              await tester.pageBack();
            }
            await tester.pump();
            _phase('invalidated', diagnostics());
            if (pendingUrl) {
              player.urlRelease!.complete();
              await tester.pump();
            }
            expect(player.plays, pendingUrl ? 0 : 1);
            expect(player.pauses + player.stops, greaterThan(1));
            if (action != 'exit') {
              expect(find.text('nested-file.txt'), findsNothing);
            }
          } finally {
            if (player.urlRelease != null && !player.urlRelease!.isCompleted) {
              player.urlRelease!.complete();
            }
            await close(tester);
          }
        },
      );
    }
  }
  for (final mode in ['ordinary', 'private-root', 'private-sibling']) {
    testWidgets('nested file export uses combined portal policy for $mode', (
      tester,
    ) async {
      var launches = 0;
      tester.binding.defaultBinaryMessenger.setMockMethodCallHandler(
        const MethodChannel('plugins.flutter.io/url_launcher'),
        (call) async {
          if (call.method == 'launch') launches++;
          return true;
        },
      );
      addTearDown(
        () => tester.binding.defaultBinaryMessenger.setMockMethodCallHandler(
          const MethodChannel('plugins.flutter.io/url_launcher'),
          null,
        ),
      );
      await mount(
        tester,
        merge(
          rootPrivate: mode == 'private-root',
          siblingPrivate: mode == 'private-sibling',
        ),
      );
      try {
        await tester.tap(find.text('nested-file.txt'));
        await tester.pumpAndSettle();
        expect(launches, mode == 'ordinary' ? 1 : 0);
        if (mode != 'ordinary') expect(find.textContaining('外部'), findsWidgets);
      } finally {
        await close(tester);
      }
    });
  }

  Future<Map<String, dynamic>> mountTagAudio(WidgetTester tester) async {
    final wire = {
      ...SdkHarness.message(id: 'tag-audio', text: ''),
      'contentType': MessageType.custom,
      'customElem': {
        'data': jsonEncode({
          'customType': 903,
          'data': {
            'soundElem': {
              'sourceUrl': 'https://media.example.com/synthetic-tag.m4a',
              'duration': 2,
              'soundPath': '/private/never-open-this',
            },
          },
        }),
        'extension': 'unchanged-vendor-extension',
        'description': '',
      },
    };
    h.sdk.messages
      ..clear()
      ..add(wire);
    await tester.runAsync(h.start);
    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          imGatewayProvider.overrideWithValue(h.sdk.gateway),
          appRuntimeProvider.overrideWith((_) => h.sdk.runtime),
        ],
        child: MaterialApp(
          home: ChatPage(
            conversationId: 'single-alice-bob',
            playbackFactory: (failure) => playback = ChatAudioPlayback(
              onFailure: failure,
              playerFactory: () => player,
            ),
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();
    return wire;
  }

  testWidgets(
    'tag903 audio starts only on tap and keeps the custom message menu',
    (tester) async {
      final wire = await mountTagAudio(tester);
      final before = jsonEncode(wire);
      try {
        expect(find.text('标签语音'), findsOneWidget);
        expect(player.entered.isCompleted, isFalse);
        expect(player.plays, 0);
        await tester.tap(find.text('标签语音'));
        await tester.pump();
        await tester.runAsync(
          () => player.entered.future.timeout(const Duration(seconds: 2)),
        );
        await tester.pump();
        expect(player.plays, 1);
        expect(playback?.messageId, 'tag-audio');
        await tester.longPress(find.text('标签语音'));
        await tester.pumpAndSettle();
        expect(find.text('转发到聊天'), findsNothing);
        expect(find.text('复制文字'), findsOneWidget);
        expect(jsonEncode(wire), before);
        await tester.pageBack();
        await tester.pumpAndSettle();
      } finally {
        await close(tester);
      }
    },
  );

  for (final invalidation in ['expiry', 'route-exit']) {
    testWidgets('tag903 pending audio cannot start after $invalidation', (
      tester,
    ) async {
      player.urlRelease = Completer<void>();
      await mountTagAudio(tester);
      try {
        await tester.tap(find.text('标签语音'));
        await tester.pump();
        await tester.runAsync(
          () => player.entered.future.timeout(const Duration(seconds: 2)),
        );
        expect(player.plays, 0);
        if (invalidation == 'expiry') {
          h.sdk.gateway.privacyLedger.observe('single-alice-bob', {
            'clientMsgID': 'tag-audio',
            'attachedInfoElem': {
              'isPrivateChat': true,
              'hasReadTime': 1700000000000,
              'burnDuration': 30,
            },
          }, now: DateTime.now());
          await tester.pump();
        } else {
          await tester.pumpWidget(const SizedBox.shrink());
        }
        player.urlRelease!.complete();
        await tester.pump();
        expect(player.plays, 0);
        expect(h.count('sendMessage'), 0);
        if (invalidation == 'expiry') {
          expect(player.stops, greaterThan(1));
        } else {
          expect(player.events.isClosed, isTrue);
        }
      } finally {
        if (!player.urlRelease!.isCompleted) {
          player.urlRelease!.complete();
        }
        await close(tester);
      }
    });
  }
}
