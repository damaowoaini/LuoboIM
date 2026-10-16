import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/features/chat/data/chat_repository.dart';
import 'package:imapp/features/chat/presentation/chat_search_page.dart';

import '../../core/im/sdk_harness.dart';

void main() {
  testWidgets(
    'actual search hides private body before deadline and reacts to learned expiry without another search',
    (tester) async {
      final harness = SdkHarness()
        ..conversations.add(SdkHarness.conversation());
      final wall = DateTime.fromMillisecondsSinceEpoch(1700000000000);
      harness.messages.add({
        ...SdkHarness.message(
          id: 'private-result',
          text: 'private-search-body',
        ),
        'attachedInfoElem': {'isPrivateChat': true},
      });
      harness.overrideCall = (call) async {
        if (call.method == 'searchLocalMessages') {
          return jsonEncode({
            'searchResultItems': [
              {
                'conversationID': 'single-alice-bob',
                'messageList': harness.messages,
              },
            ],
            'totalCount': 1,
          });
        }
        return harness.respond(call);
      };
      await tester.runAsync(harness.start);
      final repository = ChatRepository(
        gateway: harness.gateway,
        session: harness.session,
        conversationId: 'single-alice-bob',
        now: () => wall,
      );
      try {
        await tester.pumpWidget(
          MaterialApp(home: ChatSearchPage(repository: repository)),
        );
        await tester.enterText(find.byType(TextField), 'search');
        await tester.pump(const Duration(milliseconds: 400));
        await tester.pumpAndSettle();
        expect(find.text('private-search-body'), findsNothing);
        expect(find.text('[阅后即焚消息]'), findsOneWidget);
        harness.messages.single['attachedInfoElem'] = {
          'isPrivateChat': true,
          'burnDuration': 30,
          'hasReadTime': 1699999960000,
        };
        await tester.runAsync(repository.history);
        await tester.pump();
        await tester.pump();
        expect(find.text('private-search-body'), findsNothing);
        expect(find.text('阅后即焚内容已到期'), findsOneWidget);
        expect(
          harness.calls.where((call) => call.method == 'searchLocalMessages'),
          hasLength(1),
        );
      } finally {
        await tester.runAsync(harness.dispose);
        await tester.pumpWidget(const SizedBox.shrink());
      }
    },
  );
}
