import 'dart:async';
import 'dart:ui' as ui;

import 'package:file_selector/file_selector.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:image_picker/image_picker.dart';
import 'package:mobile_scanner/mobile_scanner.dart';
import 'package:permission_handler/permission_handler.dart';
import 'package:qr_flutter/qr_flutter.dart';
import 'package:share_plus/share_plus.dart';

enum QrCameraPermission { granted, denied, permanentlyDenied, unavailable }
enum QrExportResult { completed, cancelled, unavailable }

abstract interface class QrCamera {
  Widget preview(void Function() onError);
  Future<void> start(void Function(List<String>) onCodes);
  Future<void> stop();
  Future<void> toggleTorch();
  Future<List<String>> analyzeImage(String path);
  Future<void> dispose();
}

/// Tests replace OS/plugin transports, not the scan page or discovery policy.
abstract interface class QrPlatform {
  QrCamera createCamera();
  Future<QrCameraPermission> cameraPermission();
  Future<bool> openCameraSettings();
  Future<XFile?> pickImage();
  Future<Uint8List> renderPng(String data);
  Future<QrExportResult> sharePng(Uint8List bytes, Rect origin);
  bool get supportsSave;
  Future<QrExportResult> savePng(Uint8List bytes, bool Function() current);
}

final qrPlatformProvider = Provider<QrPlatform>((_) => const NativeQrPlatform());

final class NativeQrPlatform implements QrPlatform {
  const NativeQrPlatform();
  @override
  QrCamera createCamera() => _MobileQrCamera();
  @override
  Future<QrCameraPermission> cameraPermission() async {
    final status = await Permission.camera.request();
    if (status.isGranted) return QrCameraPermission.granted;
    if (status.isPermanentlyDenied || status.isRestricted) {
      return QrCameraPermission.permanentlyDenied;
    }
    return QrCameraPermission.denied;
  }
  @override
  Future<bool> openCameraSettings() => openAppSettings();
  @override
  Future<XFile?> pickImage() => ImagePicker().pickImage(
    source: ImageSource.gallery, requestFullMetadata: false,
  );
  @override
  Future<Uint8List> renderPng(String data) async {
    final recorder = ui.PictureRecorder();
    final canvas = Canvas(recorder)..drawRect(const Rect.fromLTWH(0, 0, 1024, 1024), Paint()..color = Colors.white);
    canvas.translate(64, 64);
    QrPainter(data: data, version: QrVersions.auto, gapless: true).paint(canvas, const Size(896, 896));
    final picture = recorder.endRecording();
    ui.Image? image;
    try {
      image = await picture.toImage(1024, 1024);
      final bytes = await image.toByteData(format: ui.ImageByteFormat.png);
      if (bytes == null) throw StateError('QR image rendering unavailable');
      return bytes.buffer.asUint8List(bytes.offsetInBytes, bytes.lengthInBytes);
    } finally { image?.dispose(); picture.dispose(); }
  }
  @override
  Future<QrExportResult> sharePng(Uint8List bytes, Rect origin) async {
    final result = await SharePlus.instance.share(ShareParams(
      files: [XFile.fromData(bytes, mimeType: 'image/png', name: 'luoboduihua-qr.png')],
      fileNameOverrides: const ['luoboduihua-qr.png'],
      sharePositionOrigin: origin,
    ));
    return switch (result.status) {
      ShareResultStatus.success => QrExportResult.completed,
      ShareResultStatus.dismissed => QrExportResult.cancelled,
      ShareResultStatus.unavailable => QrExportResult.unavailable,
    };
  }
  @override
  bool get supportsSave => !kIsWeb && const {
    TargetPlatform.macOS, TargetPlatform.windows, TargetPlatform.linux,
  }.contains(defaultTargetPlatform);
  @override
  Future<QrExportResult> savePng(Uint8List bytes, bool Function() current) async {
    if (!supportsSave || !current()) return QrExportResult.unavailable;
    final location = await getSaveLocation(suggestedName: 'luoboduihua-qr.png',
      acceptedTypeGroups: const [XTypeGroup(label: 'PNG 图片', extensions: ['png'])]);
    if (location == null) return QrExportResult.cancelled;
    if (!current()) return QrExportResult.cancelled;
    // Selection is an OS await. Never write under a renewed account/view lease.
    await XFile.fromData(bytes, mimeType: 'image/png').saveTo(location.path);
    return QrExportResult.completed;
  }
}

final class _MobileQrCamera implements QrCamera {
  final controller = MobileScannerController(autoStart: false,
    formats: const [BarcodeFormat.qrCode], detectionSpeed: DetectionSpeed.noDuplicates);
  StreamSubscription<BarcodeCapture>? _codes;
  Future<void> _tail = Future.value();
  bool _disposed = false, _previewFailed = false;
  Future<void> _serial(Future<void> Function() action) {
    final next = _tail.then((_) => action());
    _tail = next.catchError((Object _) {});
    return next;
  }
  Future<T> _serialValue<T>(Future<T> Function() action) {
    final next = _tail.then((_) => action());
    _tail = next.then<void>((_) {}, onError: (Object _, StackTrace _) {});
    return next;
  }
  @override
  Widget preview(void Function() onError) => MobileScanner(controller: controller,
    useAppLifecycleState: false,
    errorBuilder: (_, _) {
      if (!_disposed && !_previewFailed) {
        _previewFailed = true;
        // The plugin reports preview errors during build. Retire the page's
        // current capture after that frame, never mutate its state in build.
        WidgetsBinding.instance.addPostFrameCallback((_) { if (!_disposed) onError(); });
      }
      return const Center(child: Text('相机暂不可用，请检查权限后重试'));
    });
  @override
  Future<void> start(void Function(List<String>) onCodes) => _serial(() async {
    if (_disposed) return;
    _previewFailed = false;
    await _codes?.cancel();
    _codes = controller.barcodes.listen((capture) {
      if (!_disposed) {
        onCodes(capture.barcodes.map((b) => b.rawValue)
          .whereType<String>().where((s) => s.isNotEmpty).toList());
      }
    });
    await controller.start();
  });
  @override
  Future<void> stop() => _serial(() async {
    await _codes?.cancel(); _codes = null;
    if (!_disposed) await controller.stop();
  });
  @override
  Future<void> toggleTorch() => _serial(() async {
    if (!_disposed) await controller.toggleTorch();
  });
  @override
  Future<List<String>> analyzeImage(String path) => _serialValue(() async {
    if (_disposed) return const [];
    final capture = await controller.analyzeImage(path, formats: const [BarcodeFormat.qrCode]);
    return capture?.barcodes.map((b) => b.rawValue).whereType<String>()
      .where((s) => s.isNotEmpty).toList() ?? const [];
  });
  @override
  Future<void> dispose() => _serial(() async {
    if (_disposed) return;
    _disposed = true; await _codes?.cancel(); _codes = null;
    await controller.dispose();
  });
}
