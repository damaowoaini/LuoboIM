import 'dart:typed_data';

import 'package:dio/dio.dart';

class TestHttpAdapter implements HttpClientAdapter {
  TestHttpAdapter(this.handler);
  final Future<ResponseBody> Function(RequestOptions options) handler;
  final requests = <RequestOptions>[];
  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) {
    requests.add(options);
    return handler(options);
  }

  @override
  void close({bool force = false}) {}
}

ResponseBody jsonResponse(String body) => ResponseBody.fromString(
  body,
  200,
  headers: {
    Headers.contentTypeHeader: [Headers.jsonContentType],
  },
);
