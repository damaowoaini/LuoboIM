import 'dart:ui' as ui;

import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/features/qr/data/qr_platform.dart';

void main() {
  testWidgets('real QR painter exports a decodable PNG with an opaque white quiet zone', (tester) async {
    await tester.runAsync(() async {
      final png = await const NativeQrPlatform().renderPng('luoboduihua:render-only-fixture');
      expect(png.take(8), [137, 80, 78, 71, 13, 10, 26, 10]);
      final codec = await ui.instantiateImageCodec(png);
      try {
        final frame = await codec.getNextFrame();
        try {
          expect(frame.image.width, 1024); expect(frame.image.height, 1024);
          final pixels = await frame.image.toByteData(format: ui.ImageByteFormat.rawRgba);
          expect(pixels, isNotNull);
          expect(pixels!.buffer.asUint8List().take(4), [255, 255, 255, 255]);
        } finally { frame.image.dispose(); }
      } finally { codec.dispose(); }
    });
    expect(tester.takeException(), isNull);
  });
}
