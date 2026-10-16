import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:math';

import 'package:dio/dio.dart';
import 'package:path_provider/path_provider.dart';

import '../error/app_failure.dart';
import '../tenant/session_access.dart';

/// Downloads public/signed attachment URLs without forwarding either IM token.
/// The temporary file always belongs to the account captured at construction.
final class RemoteAttachmentStore {
  RemoteAttachmentStore(
    this.access, {
    Dio? dio,
    Future<Directory> Function()? directory,
  }) : dio =
           dio ??
           Dio(
             BaseOptions(
               connectTimeout: const Duration(seconds: 15),
               receiveTimeout: const Duration(seconds: 60),
             ),
           ),
       directory = directory ?? getTemporaryDirectory {
    invalidations = access.session.invalidations.listen((_) => cancel());
  }
  final SessionAccess access;
  final Dio dio;
  final Future<Directory> Function() directory;
  late final StreamSubscription<int> invalidations;
  final tokens = <CancelToken>{};
  bool disposed = false;

  Future<File> download(
    Uri uri, {
    required String name,
    void Function(int received, int? total)? progress,
    void Function()? checkSource,
  }) async {
    void check() {
      access.check();
      checkSource?.call();
    }

    check();
    if (disposed) throw const AppFailure(FailureKind.cancelled);
    final token = CancelToken();
    tokens.add(token);
    File? file;
    IOSink? sink;
    try {
      Response<ResponseBody>? response;
      for (var redirect = 0; redirect <= 5; redirect++) {
        check();
        if (!isSafeAttachmentUri(uri)) {
          throw const AppFailure(FailureKind.invalidInput);
        }
        response = await dio.getUri<ResponseBody>(
          uri,
          options: Options(
            responseType: ResponseType.stream,
            followRedirects: false,
            validateStatus: (status) =>
                status != null && status >= 200 && status < 400,
          ),
          cancelToken: token,
        );
        check();
        final status = response.statusCode!;
        if (status >= 300) {
          final location = response.headers.value('location');
          await response.data?.stream.listen(null).cancel();
          if (location == null || redirect == 5) {
            throw const AppFailure(FailureKind.invalidResponse);
          }
          uri = uri.resolve(location);
          continue;
        }
        break;
      }
      final body = response?.data;
      if (body == null) throw const AppFailure(FailureKind.invalidResponse);
      final root = await directory();
      check();
      final nonce = List.generate(
        12,
        (_) => Random.secure().nextInt(256).toRadixString(16).padLeft(2, '0'),
      ).join();
      final folder = Directory(
        '${root.path}/remote-attachments/${access.credentials.accountKey}/$nonce',
      );
      await folder.create(recursive: true);
      check();
      file = File('${folder.path}/${safeAttachmentFilename(name)}');
      sink = file.openWrite();
      sink.done.ignore();
      final total = int.tryParse(
        response!.headers.value('content-length') ?? '',
      );
      var received = 0;
      await for (final chunk in body.stream) {
        check();
        if (token.isCancelled) throw const AppFailure(FailureKind.cancelled);
        sink.add(chunk);
        // Bound memory when a fast socket writes to a slow device.
        await sink.flush();
        received += chunk.length;
        progress?.call(received, total);
      }
      check();
      if (token.isCancelled) throw const AppFailure(FailureKind.cancelled);
      // Decompression can change the wire length, so only compare raw bodies.
      if (total != null &&
          response.headers.value('content-encoding') == null &&
          total != received) {
        throw const AppFailure(FailureKind.invalidResponse);
      }
      await sink.close();
      sink = null;
      check();
      return file;
    } catch (error) {
      try {
        await sink?.close();
      } catch (_) {}
      try {
        if (file != null && await file.exists()) await file.delete();
      } catch (_) {}
      if (!access.isCurrent || token.isCancelled || disposed) {
        throw const AppFailure(FailureKind.cancelled);
      }
      if (error is AppFailure) rethrow;
      if (error is DioException) throw const AppFailure(FailureKind.network);
      throw const AppFailure(FailureKind.localStorage);
    } finally {
      tokens.remove(token);
      token.cancel();
    }
  }

  void cancel() {
    for (final token in tokens) {
      token.cancel();
    }
  }

  void dispose() {
    if (disposed) return;
    disposed = true;
    cancel();
    unawaited(invalidations.cancel());
    dio.close(force: true);
  }
}

String safeAttachmentFilename(String name) {
  var value = name
      .split(RegExp(r'[/\\]'))
      .last
      .replaceAll(RegExp(r'[\x00-\x1f\x7f]'), '')
      .trim();
  value = String.fromCharCodes(value.runes.take(180));
  while (utf8.encode(value).length > 180) {
    value = String.fromCharCodes(value.runes.take(value.runes.length - 1));
  }
  return value.isEmpty || value == '.' || value == '..' ? '附件.bin' : value;
}

bool isSafeAttachmentUri(Uri uri) =>
    uri.scheme == 'https' &&
    uri.host.isNotEmpty &&
    uri.userInfo.isEmpty &&
    !uri.hasFragment;
