import 'dart:io';
import 'dart:math';
import 'dart:ui' as ui;

import '../../../core/error/app_failure.dart';
import '../../../core/im/open_im_gateway.dart';
import '../../../core/storage/object_uploader.dart';
import '../../../core/tenant/session_access.dart';
import '../domain/moment.dart';

final class StagedMomentMedia {
  StagedMomentMedia({
    required this.file,
    required this.video,
    required this.width,
    required this.height,
    required this.contentType,
    required this.extension,
    this.thumbnail,
  });
  final File file;
  final File? thumbnail;
  final bool video;
  final int width, height;
  final String contentType, extension;
  Uri? _original, _thumb;
  Future<MomentMedia> upload(
    ObjectUploader uploader, {
    void Function(double)? progress,
  }) async {
    _original ??= await uploader.upload(
      file,
      contentType: contentType,
      extension: extension,
      progress: (value) =>
          progress?.call(value * (thumbnail == null ? 1 : .85)),
    );
    if (thumbnail != null) {
      _thumb ??= await uploader.upload(
        thumbnail!,
        contentType: 'image/jpeg',
        extension: '.jpg',
        progress: (value) => progress?.call(.85 + value * .15),
      );
    }
    progress?.call(1);
    return MomentMedia(
      original: _original.toString(),
      thumbnail: _thumb?.toString() ?? '',
      width: width,
      height: height,
    );
  }

  Future<void> dispose() async {
    for (final source in [file, thumbnail]) {
      try {
        if (source != null && await source.exists()) await source.delete();
      } catch (_) {}
    }
  }
}

final class MomentMediaPreparer {
  MomentMediaPreparer(this.gateway, this.access);
  final OpenImGateway gateway;
  final SessionAccess access;
  static final _random = Random.secure();
  Future<StagedMomentMedia> prepare(
    String sourcePath, {
    required bool video,
  }) async {
    access.check();
    final source = File(sourcePath);
    final extension =
        RegExp(r'\.[a-zA-Z0-9]{1,10}$')
            .firstMatch(sourcePath)
            ?.group(0)
            ?.toLowerCase() ??
        '';
    const imageTypes = {
      '.jpg': 'image/jpeg',
      '.jpeg': 'image/jpeg',
      '.png': 'image/png',
      '.webp': 'image/webp',
      '.heic': 'image/heic',
      '.heif': 'image/heif',
    };
    const videoTypes = {
      '.mp4': 'video/mp4',
      '.mov': 'video/quicktime',
      '.m4v': 'video/x-m4v',
    };
    final type = (video ? videoTypes : imageTypes)[extension];
    if (type == null) throw const AppFailure(FailureKind.invalidInput);
    File? staged, thumbnail;
    try {
      final size = await source.length();
      access.check();
      if (size < 1 || size > (video ? 100 : 20) * 1024 * 1024) {
        throw const AppFailure(FailureKind.invalidInput);
      }
      final directory = await gateway.mediaDirectory(access.generation);
      access.check();
      final stem =
          'moment_${List.generate(16, (_) => _random.nextInt(256).toRadixString(16).padLeft(2, '0')).join()}';
      staged = await source.copy('${directory.path}/$stem$extension');
      access.check();
      int width, height;
      if (video) {
        thumbnail = File('${directory.path}/$stem.jpg');
        final meta = await gateway.runForSession(
          access.generation,
          (manager) => manager.localVideoMetadata(
            videoPath: staged!.path,
            thumbnailPath: thumbnail!.path,
          ),
        );
        access.check();
        final duration = meta['durationMilliseconds'];
        if (duration is! num ||
            !duration.isFinite ||
            duration <= 0 ||
            duration > 15000 ||
            meta['thumbnailPath'] != thumbnail.path) {
          throw const AppFailure(FailureKind.invalidInput);
        }
        final dimensions = await _dimensions(thumbnail);
        width = dimensions.$1;
        height = dimensions.$2;
      } else {
        final dimensions = await _dimensions(staged);
        width = dimensions.$1;
        height = dimensions.$2;
      }
      access.check();
      return StagedMomentMedia(
        file: staged,
        video: video,
        width: width,
        height: height,
        contentType: type,
        extension: extension,
        thumbnail: thumbnail,
      );
    } catch (error) {
      for (final file in [staged, thumbnail]) {
        try {
          if (file != null && await file.exists()) await file.delete();
        } catch (_) {}
      }
      if (error is AppFailure) rethrow;
      throw const AppFailure(FailureKind.localStorage);
    }
  }

  Future<(int, int)> _dimensions(File file) async {
    final buffer = await ui.ImmutableBuffer.fromFilePath(file.path);
    ui.ImageDescriptor? descriptor;
    try {
      descriptor = await ui.ImageDescriptor.encoded(buffer);
      if (descriptor.width < 1 ||
          descriptor.height < 1 ||
          descriptor.width * descriptor.height > 100000000) {
        throw const AppFailure(FailureKind.invalidInput);
      }
      return (descriptor.width, descriptor.height);
    } finally {
      descriptor?.dispose();
      buffer.dispose();
    }
  }
}
