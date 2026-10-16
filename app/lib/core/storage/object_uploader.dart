import 'dart:async';
import 'dart:io';
import 'dart:math';

import 'package:dio/dio.dart';

import '../error/app_failure.dart';
import '../network/tenant_api.dart';

/// Authenticated metadata stays on IM; a separate client uploads signed forms
/// without carrying either chat or IM credentials to the storage provider.
final class ObjectUploader {
  ObjectUploader(this.api, {Dio? storage})
    : _storage =
          storage ??
          Dio(BaseOptions(connectTimeout: const Duration(seconds: 15))) {
    _invalidation = api.access.session.invalidations.listen((_) => close());
  }
  final TenantApi api;
  final Dio _storage;
  late final StreamSubscription<int> _invalidation;
  final _pending = <CancelToken>{};
  bool _closed = false;
  static final _random = Random.secure();

  void _check() {
    api.access.check();
    if (_closed) throw const AppFailure(FailureKind.cancelled);
  }

  Uri _url(Object? value) {
    final uri = value is String ? Uri.tryParse(value) : null;
    if (uri == null ||
        uri.scheme != 'https' ||
        uri.host.isEmpty ||
        uri.userInfo.isNotEmpty ||
        uri.hasFragment) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    return uri;
  }

  Map<String, Object?> _map(Object? value) {
    if (value is! Map || value.keys.any((key) => key is! String)) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    return Map<String, Object?>.from(value);
  }

  Future<Uri> upload(
    File source, {
    required String contentType,
    String extension = '',
    String group = 'moments',
    void Function(double fraction)? progress,
    void Function()? checkSource,
  }) async {
    void check() {
      _check();
      checkSource?.call();
    }

    check();
    if (!RegExp(r'^[a-z]+/[a-zA-Z0-9.+-]+$').hasMatch(contentType) ||
        !RegExp(r'^(\.[a-zA-Z0-9]{1,10})?$').hasMatch(extension) ||
        !RegExp(r'^[a-z][a-z0-9_-]{0,31}$').hasMatch(group)) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    final int size;
    try {
      size = await source.length();
    } on FileSystemException {
      throw const AppFailure(FailureKind.localStorage);
    }
    check();
    if (size < 1 || size > 100 * 1024 * 1024) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    final unique = List.generate(
      16,
      (_) => _random.nextInt(256).toRadixString(16).padLeft(2, '0'),
    ).join();
    final filename = '$unique$extension';
    final name = '${api.access.credentials.userId}/$group/$filename';
    final raw = await api.im.post('object/initiate_form_data', {
      'name': name,
      'size': size,
      'contentType': contentType,
      'group': group,
    });
    check();
    final upload = _map(raw);
    final url = _url(upload['url']);
    final id = upload['id'], fileKey = upload['file'];
    if (id is! String ||
        id.isEmpty ||
        fileKey is! String ||
        !RegExp(r'^[a-zA-Z0-9_-]{1,64}$').hasMatch(fileKey) ||
        upload['successCodes'] is! List) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    final fields = _map(upload['formData']);
    if (fields.length > 100 ||
        fields.containsKey(fileKey) ||
        fields.values.any((value) => value is! String) ||
        fields.keys.any(
          (key) => !RegExp(r'^[a-zA-Z0-9_.-]{1,128}$').hasMatch(key),
        )) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    final headers = <String, String>{};
    final rawHeaders = upload['header'];
    if (rawHeaders != null && rawHeaders is! List) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    for (final value in (rawHeaders as List? ?? const [])) {
      final header = _map(value),
          key = header['key'],
          values = header['values'];
      if (key is! String ||
          values is! List ||
          values.length != 1 ||
          values.single is! String ||
          !RegExp(r'^[a-zA-Z0-9-]+$').hasMatch(key) ||
          (values.single as String).contains(RegExp(r'[\r\n]')) ||
          [
            'token',
            'authorization',
            'cookie',
            'host',
            'content-length',
          ].contains(key.toLowerCase())) {
        throw const AppFailure(FailureKind.invalidResponse);
      }
      headers[key] = values.single as String;
    }
    final MultipartFile file;
    try {
      file = await MultipartFile.fromFile(
        source.path,
        filename: filename,
        contentType: DioMediaType.parse(contentType),
      );
    } on FileSystemException {
      throw const AppFailure(FailureKind.localStorage);
    }
    check();
    final cancel = CancelToken();
    _pending.add(cancel);
    ResponseBody? providerBody;
    try {
      final response = await _storage.postUri<ResponseBody>(
        url,
        data: FormData.fromMap({...fields, fileKey: file}),
        cancelToken: cancel,
        options: Options(
          headers: headers,
          followRedirects: false,
          maxRedirects: 0,
          responseType: ResponseType.stream,
          sendTimeout: const Duration(minutes: 5),
          receiveTimeout: const Duration(seconds: 30),
        ),
        onSendProgress: (sent, total) {
          if (!_closed && api.access.isCurrent && total > 0) {
            progress?.call((sent / total).clamp(0, 1));
          }
        },
      );
      providerBody = response.data;
      check();
      final codes = upload['successCodes'] as List;
      if (codes.isEmpty ||
          codes.any((value) => value is! int) ||
          !codes.contains(response.statusCode)) {
        throw const AppFailure(FailureKind.invalidResponse);
      }
      // Storage success bodies are unused; close the stream without retaining
      // arbitrary provider HTML/XML or evaluating it as application content.
      check();
      final complete = _map(
        await api.im.post('object/complete_form_data', {'id': id}),
      );
      check();
      return _url(complete['url']);
    } on DioException catch (error) {
      check();
      throw switch (error.type) {
        DioExceptionType.cancel => const AppFailure(FailureKind.cancelled),
        DioExceptionType.connectionTimeout ||
        DioExceptionType.receiveTimeout ||
        DioExceptionType.sendTimeout => const AppFailure(FailureKind.timeout),
        _ => const AppFailure(FailureKind.network),
      };
    } on FileSystemException {
      throw const AppFailure(FailureKind.localStorage);
    } finally {
      _pending.remove(cancel);
      // Cancelling Dio's wrapper stream alone leaves its upstream receive
      // timer/connection alive. Cancel this request as well after completion.
      cancel.cancel();
      try {
        await providerBody?.stream
            .listen((_) {}, onError: (Object _) {})
            .cancel();
      } catch (_) {
        // Never surface provider response bodies or transport details.
      }
    }
  }

  void close() {
    if (_closed) return;
    _closed = true;
    for (final request in _pending.toList()) {
      request.cancel();
    }
    _storage.close(force: true);
    unawaited(_invalidation.cancel());
  }
}
