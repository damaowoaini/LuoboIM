import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'global_search_fixture.dart';

void main() {
  testWidgets('primary inbox entry reaches the mounted complete global search', (tester) async {
    final f = GlobalFixture();
    try {
      await f.mount(tester, inbox: true);
      expect(find.byTooltip('全局搜索'), findsOneWidget);
      await tester.tap(find.byTooltip('全局搜索'));
      await tester.pumpAndSettle();
      expect(find.text('全局搜索'), findsOneWidget);
      expect(find.byType(Tab), findsNWidgets(5));
      expect(f.calls('searchLocalMessages'), 0);
    } finally { await f.close(tester); }
  });
  testWidgets('existing explicitly named inbox filter remains available', (tester) async {
    final f = GlobalFixture();
    try {
      await f.mount(tester, inbox: true);
      await tester.tap(find.byTooltip('搜索当前会话'));
      await tester.pumpAndSettle();
      await tester.enterText(find.byType(TextField), 'no-such-inbox');
      await tester.pump();
      expect(find.text('没有找到匹配的会话'), findsOneWidget);
      expect(f.calls('searchLocalMessages'), 0);
    } finally { await f.close(tester); }
  });
}
