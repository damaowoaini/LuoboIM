import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/features/personal/domain/personal_item.dart';
import 'package:imapp/features/personal/presentation/personal_page.dart';

void main() {
  testWidgets(
    'failed durable save keeps the draft on screen and allows retry',
    (tester) async {
      var attempts = 0;
      final initial = PersonalItem.create(PersonalKind.note)
          .copyWith(title: '原内容');
      await tester.pumpWidget(
        MaterialApp(
          home: PersonalEditor(
            item: initial,
            onSave: (_) async {
              attempts++;
              throw StateError('fixture disk full');
            },
          ),
        ),
      );
      await tester.enterText(find.byType(TextField).first, '不能丢失的修改');
      await tester.tap(find.text('保存'));
      await tester.pumpAndSettle();
      expect(find.text('不能丢失的修改'), findsOneWidget);
      expect(find.text('保存失败，内容已保留在此页面，请重试'), findsOneWidget);
      expect(attempts, 1);
      await tester.tap(find.text('保存'));
      await tester.pumpAndSettle();
      expect(attempts, 2);
      expect(find.byType(PersonalEditor), findsOneWidget);
    },
  );
  testWidgets('leaving an edited note asks before discarding', (tester) async {
    await tester.pumpWidget(
      MaterialApp(
        home: Builder(
          builder: (context) => TextButton(
            onPressed: () => Navigator.push<void>(
              context,
              MaterialPageRoute(
                builder: (_) => PersonalEditor(
                  item: PersonalItem.create(PersonalKind.note),
                  onSave: (_) async {},
                ),
              ),
            ),
            child: const Text('打开'),
          ),
        ),
      ),
    );
    await tester.tap(find.text('打开'));
    await tester.pumpAndSettle();
    await tester.enterText(find.byType(TextField).first, '未保存的笔记');
    await tester.pumpAndSettle();
    await tester.pageBack();
    await tester.pumpAndSettle();
    expect(find.text('放弃未保存的修改？'), findsOneWidget);
    await tester.tap(find.text('继续编辑'));
    await tester.pumpAndSettle();
    expect(find.text('未保存的笔记'), findsOneWidget);
  });
}
