import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/features/chat/domain/message_privacy.dart';
import 'package:imapp/features/chat/presentation/message_privacy_content.dart';

class DisposableMedia extends StatefulWidget {
  const DisposableMedia({super.key, required this.closed});
  final VoidCallback closed;
  @override
  State<DisposableMedia> createState() => _DisposableMediaState();
}

class _DisposableMediaState extends State<DisposableMedia> {
  @override
  void dispose() {
    widget.closed();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => const Text('private-media');
}

void main() {
  testWidgets(
    'content already hidden at expiry cannot reappear when the device clock moves backwards',
    (tester) async {
      var now = DateTime.fromMillisecondsSinceEpoch(1700000001000);
      await tester.pumpWidget(
        MaterialApp(
          home: MessagePrivacyContent(
            privacy: const MessagePrivacy(
              containsPrivateContent: true,
              expiresAtMilliseconds: 1700000001000,
            ),
            now: () => now,
            child: const Text('expired-private-body'),
          ),
        ),
      );
      expect(find.text('expired-private-body'), findsNothing);
      now = now.subtract(const Duration(minutes: 10));
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
      await tester.pump();
      expect(find.text('expired-private-body'), findsNothing);
      await tester.pumpWidget(const SizedBox.shrink());
    },
  );
  testWidgets(
    'known expiry hides content and disposes an open media child instead of claiming native deletion',
    (tester) async {
      var now = DateTime.fromMillisecondsSinceEpoch(1700000000000);
      var closed = 0;
      await tester.pumpWidget(
        MaterialApp(
          home: MessagePrivacyContent(
            privacy: const MessagePrivacy(
              containsPrivateContent: true,
              expiresAtMilliseconds: 1700000001000,
            ),
            now: () => now,
            child: DisposableMedia(closed: () => closed++),
          ),
        ),
      );
      expect(find.text('private-media'), findsOneWidget);
      now = now.add(const Duration(seconds: 1));
      await tester.pump(const Duration(milliseconds: 1001));
      expect(find.text('private-media'), findsNothing);
      expect(find.text('阅后即焚内容已到期'), findsOneWidget);
      expect(closed, 1);
      await tester.pumpWidget(const SizedBox.shrink());
    },
  );
  testWidgets(
    'resume immediately rechecks known expiry even when the background clock advanced without Dart timers',
    (tester) async {
      var now = DateTime.fromMillisecondsSinceEpoch(1700000000000);
      await tester.pumpWidget(
        MaterialApp(
          home: MessagePrivacyContent(
            privacy: const MessagePrivacy(
              containsPrivateContent: true,
              expiresAtMilliseconds: 1700000030000,
            ),
            now: () => now,
            child: const Text('private-body'),
          ),
        ),
      );
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
      now = now.add(const Duration(minutes: 2));
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
      await tester.pump();
      expect(find.text('private-body'), findsNothing);
      expect(find.text('阅后即焚内容已到期'), findsOneWidget);
      await tester.pumpWidget(const SizedBox.shrink());
    },
  );
}
