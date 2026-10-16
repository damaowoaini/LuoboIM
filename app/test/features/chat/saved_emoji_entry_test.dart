import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import '../../core/im/sdk_harness.dart';
import 'chat_font_fixture.dart';

/// Same test source is usable against the parent six production files: it
/// imports no new library API. Missing entries must be actual widget assertions,
/// never missing-method/fixture compilation errors in a before run.
void main() {
  testWidgets(
    'actual chat emoji button exposes saved image library while keeping Unicode insertion',
    (tester) async {
      final f = ChatFontFixture();
      try {
        await f.mount(tester, chat: true);
        await tester.tap(find.byTooltip('插入表情').hitTestable());
        await f.ready(tester, find.byTooltip('😀'));
        await tester.tap(find.byTooltip('😀').hitTestable());
        await f.frame(tester);
        expect(find.widgetWithText(TextField, '😀'), findsOneWidget);
        await tester.tap(find.byTooltip('插入表情').hitTestable());
        await f.ready(tester, find.byTooltip('😀'));
        expect(find.text('常用表情'), findsOneWidget);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'actual normal image message menu has a separate add saved emoji action and keeps ordinary favorite',
    (tester) async {
      final f = ChatFontFixture();
      try {
        await tester.runAsync(() async {
          await f.start();
          f.sdk.messages.add({
            ...SdkHarness.message(id: 'entry-image', text: ''),
            'contentType': 102,
            'pictureElem': {
              'sourcePicture': {
                'url': 'https://objects.example.com/entry.png',
                'width': 1,
                'height': 1,
              },
            },
          });
        });
        await f.mount(tester, chat: true);
        final image = find.bySemanticsLabel('图片消息');
        await tester.ensureVisible(image);
        await f.ready(tester, image);
        await tester.longPress(image.hitTestable());
        await f.ready(tester, find.text('收藏'));
        expect(find.text('收藏'), findsOneWidget);
        expect(find.text('加入常用表情'), findsOneWidget);
      } finally {
        await f.close(tester);
      }
    },
  );
}
