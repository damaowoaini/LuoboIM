import 'package:flutter/material.dart';
import 'package:flutter_openim_sdk/flutter_openim_sdk.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/features/chat/data/message_mapper.dart';
import 'package:imapp/features/chat/presentation/chat_merged_messages_page.dart';

import '../../core/im/sdk_harness.dart';
import 'batch_harness.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  Map<String, dynamic> root(int count) => {
    'clientMsgID': 'root',
    'contentType': 107,
    'mergeElem': {
      'multiMessage': [
        for (var i = 0; i < count; i++)
          SdkHarness.message(id: 'child$i', text: 'child-body$i'),
      ],
    },
  };
  test('standard107 projects all20 ordinary child types and rejects retaining a truncated larger remote merge', () {
    final complete = mapMessage(Message.fromJson(root(20)), 'alice');
    expect(complete.mergedMessages, hasLength(20));
    expect(complete.mergedContentComplete, true);
    final truncated = mapMessage(Message.fromJson(root(21)), 'alice');
    expect(truncated.mergedMessages, hasLength(20));
    expect(truncated.mergedContentComplete, false);
    expect(truncated.canForward, false);
  });
  test('nested merge projection has a global bounded node budget and keeps known private descendants denied', () {
    var wire = root(1);
    for (var i = 0; i < 8; i++) {
      wire = {
        'clientMsgID': 'nested$i',
        'contentType': 107,
        'mergeElem': {
          'multiMessage': [wire],
        },
      };
    }
    final bounded = mapMessage(Message.fromJson(wire), 'alice');
    expect(bounded.mergedContentComplete, false);
    expect(bounded.canForward, false);
    final private = root(2);
    (((private['mergeElem'] as Map)['multiMessage'] as List).last
        as Map)['attachedInfoElem'] = {
      'isPrivateChat': true,
    };
    final protected = mapMessage(Message.fromJson(private), 'alice');
    expect(protected.privacy.allowsRetention, false);
  });
  for (final ownerEnded in [false, true]) {
    testWidgets(
      'an already pushed merge viewer removes child bodies when ${ownerEnded ? 'its owner ends' : 'a child is later known expired'}',
      (tester) async {
        final h = BatchHarness();
        await tester.runAsync(h.start);
        try {
          final wire = root(2);
          final policy = h.sdk.gateway.privacyLedger.observe(
            'single-alice-bob',
            wire,
            now: DateTime.now(),
          );
          final mapped = mapMessage(
            Message.fromJson(wire),
            'alice',
            privacy: policy,
          );
          await tester.pumpWidget(
            MaterialApp(
              home: Builder(
                builder: (context) => Scaffold(
                  body: TextButton(
                    onPressed: () => Navigator.of(context).push(
                      MaterialPageRoute<void>(
                        builder: (_) => ChatMergedMessagesPage(
                          root: mapped,
                          messages: mapped.mergedMessages,
                          canView: () => h.source.canView(mapped),
                          childBuilder: (m, _) => Text(m.text),
                        ),
                      ),
                    ),
                    child: const Text('打开记录'),
                  ),
                ),
              ),
            ),
          );
          await tester.tap(find.text('打开记录'));
          await tester.pumpAndSettle();
          expect(find.text('child-body0'), findsOneWidget);
          if (ownerEnded) {
            await tester.runAsync(h.sdk.session.signOut);
          } else {
            h.sdk.gateway.privacyLedger.observe('single-alice-bob', {
              'clientMsgID': 'child0',
              'attachedInfoElem': {
                'isPrivateChat': true,
                'hasReadTime': 1700000000000,
                'burnDuration': 30,
              },
            }, now: DateTime.now());
          }
          await tester.pumpAndSettle();
          expect(find.text('child-body0'), findsNothing);
          expect(find.text('child-body1'), findsNothing);
        } finally {
          await tester.runAsync(h.dispose);
          await tester.pumpWidget(const SizedBox.shrink());
        }
      },
    );
  }
}
