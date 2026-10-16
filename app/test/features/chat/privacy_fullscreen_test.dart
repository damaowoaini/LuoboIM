import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/privacy/message_privacy.dart';
import 'package:imapp/features/chat/presentation/message_privacy_content.dart';

final class _MediaProbe extends StatefulWidget {
  const _MediaProbe(this.open, this.close);
  final VoidCallback open, close;
  @override
  State<_MediaProbe> createState() => _MediaProbeState();
}

final class _MediaProbeState extends State<_MediaProbe> {
  @override
  void initState() {
    super.initState();
    widget.open();
  }

  @override
  void dispose() {
    widget.close();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => const Text('inline-media-probe');
}

void main() {
  for (final event in ['expired', 'owner-ended']) {
    testWidgets(
      'a Navigator fullscreen route disposes unknown-time media on $event without its parent rebuilding',
      (tester) async {
        final ledger = MessagePrivacyLedger()..bind('A', 1);
        final wall = DateTime.fromMillisecondsSinceEpoch(1700000000000);
        final privacy = ledger.observe('c', {
          'clientMsgID': 'm',
          'attachedInfoElem': {'isPrivateChat': true},
        }, now: wall);
        final navigator = GlobalKey<NavigatorState>();
        var opened = 0, closed = 0;
        await tester.pumpWidget(
          MaterialApp(
            navigatorKey: navigator,
            home: const Scaffold(body: Text('parent')),
          ),
        );
        navigator.currentState!.push(
          MaterialPageRoute<void>(
            builder: (_) => Scaffold(
              body: MessagePrivacyContent(
                privacy: privacy,
                now: () => wall,
                child: _MediaProbe(() => opened++, () => closed++),
              ),
            ),
          ),
        );
        await tester.pumpAndSettle();
        expect(opened, 1);
        expect(find.text('inline-media-probe'), findsOneWidget);
        if (event == 'expired') {
          ledger.observe('c', {
            'clientMsgID': 'm',
            'attachedInfoElem': {
              'isPrivateChat': true,
              'burnDuration': 30,
              'hasReadTime': 1699999960000,
            },
          }, now: wall);
        } else {
          ledger.endSession();
        }
        await tester.pump();
        await tester.pump();
        expect(closed, 1);
        expect(opened, 1);
        expect(find.text('inline-media-probe'), findsNothing);
        await tester.pumpWidget(const SizedBox.shrink());
        await ledger.dispose();
      },
    );
  }
}
