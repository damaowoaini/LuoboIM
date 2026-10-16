import 'dart:io';
import 'dart:math';
import 'dart:ui' as ui;

import '../error/app_failure.dart';
import '../im/open_im_gateway.dart';
import '../tenant/session_access.dart';

/// Only the copied private file is owned by this object, never picker originals.
final class StagedImage {
  StagedImage(
    this.file,
    this.width,
    this.height,
    this.contentType,
    this.extension,
  );
  final File file;
  final int width, height;
  final String contentType, extension;

  Future<void> dispose() async {
    try {
      if (await file.exists()) await file.delete();
    } catch (_) {}
  }
}

final class ImageStager {
  ImageStager(this.gateway, this.access);
  final OpenImGateway gateway;
  final SessionAccess access;
  static final _random = Random.secure();

  Future<StagedImage> prepare(String path) async {
    access.check();
    File? staged;
    ui.ImmutableBuffer? buffer;
    ui.ImageDescriptor? descriptor;
    try {
      final source = File(path);
      final size = await source.length();
      access.check();
      if (size < 1 || size > 20 * 1024 * 1024) {
        throw const AppFailure(FailureKind.invalidInput);
      }
      final extension = RegExp(r'\.[a-zA-Z0-9]{1,10}$')
          .firstMatch(path)
          ?.group(0)
          ?.toLowerCase();
      final contentType = const {
        '.jpg': 'image/jpeg',
        '.jpeg': 'image/jpeg',
        '.png': 'image/png',
        '.webp': 'image/webp',
        '.heic': 'image/heic',
        '.heif': 'image/heif',
      }[extension];
      if (contentType == null || extension == null) {
        throw const AppFailure(FailureKind.invalidInput);
      }
      final directory = await gateway.mediaDirectory(access.generation);
      access.check();
      final name = List.generate(
        16,
        (_) => _random.nextInt(256).toRadixString(16).padLeft(2, '0'),
      ).join();
      staged = await source.copy('${directory.path}/image_$name$extension');
      access.check();
      buffer = await ui.ImmutableBuffer.fromFilePath(staged.path);
      descriptor = await ui.ImageDescriptor.encoded(buffer);
      access.check();
      if (descriptor.width < 1 ||
          descriptor.height < 1 ||
          descriptor.width * descriptor.height > 100000000) {
        throw const AppFailure(FailureKind.invalidInput);
      }
      return StagedImage(
        staged,
        descriptor.width,
        descriptor.height,
        contentType,
        extension,
      );
    } catch (error) {
      try {
        if (staged != null && await staged.exists()) await staged.delete();
      } catch (_) {}
      if (error is AppFailure) rethrow;
      throw const AppFailure(FailureKind.localStorage);
    } finally {
      descriptor?.dispose();
      buffer?.dispose();
    }
  }
}
