import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/design/tokens.dart';

/// Real glyphs for optional local evidence captures. Regular tests remain
/// platform-independent and no operating-system font is bundled with the app.
Future<ThemeData> captureTheme(WidgetTester tester) async {
  final theme = buildAppTheme();
  if (const String.fromEnvironment('TEST_CAPTURE_DIR').isEmpty) return theme;
  await tester.runAsync(() async {
    final chinese = FontLoader('LuoboCapture');
    chinese.addFont(
      File(
        const String.fromEnvironment(
          'TEST_CAPTURE_FONT',
          defaultValue: '/System/Library/Fonts/Supplemental/Arial Unicode.ttf',
        ),
      ).readAsBytes().then((bytes) => ByteData.sublistView(bytes)),
    );
    await chinese.load();
    final icons = FontLoader('MaterialIcons');
    icons.addFont(
      File(
        const String.fromEnvironment(
          'TEST_CAPTURE_ICONS',
          defaultValue: '/Users/chuzu/.local/share/luobo-toolchains/flutter-3.47.7/bin/cache/artifacts/material_fonts/MaterialIcons-Regular.otf',
        ),
      ).readAsBytes().then((bytes) => ByteData.sublistView(bytes)),
    );
    await icons.load();
  });
  return theme.copyWith(
    textTheme: theme.textTheme.apply(fontFamily: 'LuoboCapture'),
    primaryTextTheme: theme.primaryTextTheme.apply(fontFamily: 'LuoboCapture'),
    filledButtonTheme: FilledButtonThemeData(
      style: theme.filledButtonTheme.style?.copyWith(
        textStyle: WidgetStatePropertyAll(
          (theme.filledButtonTheme.style?.textStyle?.resolve({}) ??
                  const TextStyle())
              .copyWith(fontFamily: 'LuoboCapture'),
        ),
      ),
    ),
  );
}
