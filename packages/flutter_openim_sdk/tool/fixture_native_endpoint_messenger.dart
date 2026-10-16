// Explicit local QA helper. Production application code does not import this.
import 'dart:ui' as ui;

import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';

/// Keeps the production Provider graph and main entry intact. Only the legacy
/// native core's two initialization endpoints bypass unavailable fixture DNS.
/// TLS verification remains native, using the explicitly pinned test CA.
class FixtureNativeEndpointBinding extends WidgetsFlutterBinding {
  @override
  BinaryMessenger createBinaryMessenger() =>
      FixtureNativeEndpointMessenger(super.createBinaryMessenger());
}

class FixtureNativeEndpointMessenger implements BinaryMessenger {
  FixtureNativeEndpointMessenger(this._delegate);

  final BinaryMessenger _delegate;
  static const _codec = StandardMethodCodec();
  String _endpoint(Object? value, String scheme) {
    final uri = value is String ? Uri.tryParse(value) : null;
    if (uri == null ||
        uri.scheme != scheme ||
        uri.host != 'luobo.fixture.test' ||
        (uri.hasPort && uri.port != 443) ||
        !(scheme == 'https'
            ? const {'/api', '/api/'}.contains(uri.path)
            : uri.path == '/msg_gateway') ||
        uri.userInfo.isNotEmpty ||
        uri.hasQuery ||
        uri.hasFragment) {
      throw StateError('Unexpected explicit QA native endpoint');
    }
    return uri.replace(host: '10.0.2.2', port: 34443).toString();
  }

  @override
  Future<ByteData?>? send(String channel, ByteData? message) {
    if (channel == 'jrtxa_im_sdk' && message != null) {
      final call = _codec.decodeMethodCall(message);
      if (call.method == 'initSDK') {
        if (call.arguments is! Map) {
          throw StateError('Invalid explicit QA native initialization');
        }
        final arguments = Map<Object?, Object?>.of(call.arguments as Map);
        arguments['apiAddr'] = _endpoint(arguments['apiAddr'], 'https');
        arguments['wsAddr'] = _endpoint(arguments['wsAddr'], 'wss');
        message = _codec.encodeMethodCall(MethodCall(call.method, arguments));
      }
    }
    return _delegate.send(channel, message);
  }

  @override
  void setMessageHandler(String channel, MessageHandler? handler) =>
      _delegate.setMessageHandler(channel, handler);

  @override
  Future<void> handlePlatformMessage(
    String channel,
    ByteData? data,
    ui.PlatformMessageResponseCallback? callback,
  ) =>
      // ignore: deprecated_member_use
      _delegate.handlePlatformMessage(channel, data, callback);
}
