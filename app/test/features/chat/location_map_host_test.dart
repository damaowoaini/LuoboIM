import 'dart:async';
import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/features/chat/domain/location_pick.dart';
import 'package:imapp/features/chat/presentation/chat_location_picker_page.dart';
import 'package:webview_flutter_platform_interface/webview_flutter_platform_interface.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  test('production WebView host preserves healthy picker channels for subresource HTTP errors and fails only its exact main document', () async {
    final previous = WebViewPlatform.instance;
    final platform = _HostPlatform();
    WebViewPlatform.instance = platform;
    final candidates = <String>[], states = <String>[];
    var failures = 0, retirements = 0;
    final host = WebViewLocationMapHost(
      config: LocationPickerConfig.parse(
        'https://map.example.com/picker',
        '116.4',
        '39.9',
      )!,
      nonce: 'a' * 64,
      candidate: candidates.add,
      status: states.add,
      failed: () => ++failures,
      retired: () => ++retirements,
    );
    try {
      await host.start();
      await platform.controller.initialized.future.timeout(
        const Duration(seconds: 2),
      );
      expect(
        platform.controller.loaded,
        Uri.parse('https://map.example.com/picker'),
      );
      expect(
        platform.controller.scripts.single,
        contains('LuoboMapPickerV1.init('),
      );
      final state = jsonEncode({
        'requestNonce': 'a' * 64,
        'sequence': 0,
        'state': 'ready',
      });
      final candidate = jsonEncode({
        'requestNonce': 'a' * 64,
        'sequence': 1,
        'lng': 0,
        'lat': 0,
        'name': '原点',
      });
      platform.controller.emit('LuoboMapStateV1', state);
      platform.controller.emit('LuoboMapCandidateV1', candidate);
      expect(states, [state]);
      expect(candidates, [candidate]);
      final delegate = platform.controller.delegate;
      for (final uri in [
        'https://map.example.com/picker/config',
        'https://map.example.com/_AMapService/v3/place/text',
        'https://map.example.com/_AMapService/v3/assistant/coordinate/convert',
        'https://tiles.example.com/tile/0/0/0',
        'https://foreign.example.com/picker',
      ]) {
        delegate.httpError!(
          HttpResponseError(
            request: WebResourceRequest(uri: Uri.parse(uri)),
            response: WebResourceResponse(uri: Uri.parse(uri), statusCode: 503),
          ),
        );
      }
      delegate.httpError!(const HttpResponseError());
      delegate.httpError!(
        const HttpResponseError(
          response: WebResourceResponse(uri: null, statusCode: 500),
        ),
      );
      // A known subresource request must not be relabelled using a different
      // response URI. Only a missing request may use the response fallback.
      delegate.httpError!(
        HttpResponseError(
          request: WebResourceRequest(
            uri: Uri.parse('https://map.example.com/picker/config'),
          ),
          response: WebResourceResponse(
            uri: Uri.parse('https://map.example.com/picker'),
            statusCode: 500,
          ),
        ),
      );
      expect(failures, 0);
      expect(retirements, 0);
      platform.controller.emit('LuoboMapStateV1', state);
      platform.controller.emit('LuoboMapCandidateV1', candidate);
      expect(states, [state, state]);
      expect(candidates, [candidate, candidate]);
      expect(
        await host.command('convertGps', {
          'requestNonce': 'a' * 64,
          'commandId': 1,
          'expectedSequence': 1,
          'lng': 0,
          'lat': 0,
        }),
        isTrue,
      );
      delegate.httpError!(
        HttpResponseError(
          request: WebResourceRequest(
            uri: Uri.parse('https://map.example.com/picker'),
          ),
          response: const WebResourceResponse(uri: null, statusCode: 503),
        ),
      );
      expect(failures, 1);
      delegate.httpError!(
        HttpResponseError(
          response: WebResourceResponse(
            uri: Uri.parse('https://map.example.com/picker'),
            statusCode: 500,
          ),
        ),
      );
      expect(failures, 2);
    } finally {
      await host.dispose();
      if (previous != null) {
        WebViewPlatform.instance = previous;
      }
      // The official setter rejects null. With no previous platform this
      // test's transport-only instance lives only in this test-file isolate.
    }
  });
}

// Only the OS transport is replaced. The app-facing WebViewController,
// NavigationDelegate and production WebViewLocationMapHost are all real.
final class _HostPlatform extends WebViewPlatform {
  late _HostController controller;

  @override
  PlatformWebViewController createPlatformWebViewController(
    PlatformWebViewControllerCreationParams params,
  ) => controller = _HostController(params);

  @override
  PlatformNavigationDelegate createPlatformNavigationDelegate(
    PlatformNavigationDelegateCreationParams params,
  ) => _HostDelegate(params);
}

final class _HostController extends PlatformWebViewController {
  _HostController(super.params) : super.implementation();
  late _HostDelegate delegate;
  final channels = <String, JavaScriptChannelParams>{};
  final scripts = <String>[];
  final initialized = Completer<void>();
  Uri? loaded;

  void emit(String channel, String value) =>
      channels[channel]!.onMessageReceived(JavaScriptMessage(message: value));

  @override
  Future<void> setOnPlatformPermissionRequest(
    void Function(PlatformWebViewPermissionRequest request) onPermissionRequest,
  ) async {}

  @override
  Future<void> setJavaScriptMode(JavaScriptMode javaScriptMode) async {}

  @override
  Future<void> addJavaScriptChannel(JavaScriptChannelParams params) async {
    channels[params.name] = params;
  }

  @override
  Future<void> setPlatformNavigationDelegate(
    PlatformNavigationDelegate handler,
  ) async {
    delegate = handler as _HostDelegate;
  }

  @override
  Future<void> loadRequest(LoadRequestParams params) async {
    loaded = params.uri;
    if (await delegate.navigation!(
          NavigationRequest(url: params.uri.toString(), isMainFrame: true),
        ) ==
        NavigationDecision.navigate) {
      delegate.started!(params.uri.toString());
      delegate.finished!(params.uri.toString());
    }
  }

  @override
  Future<Object> runJavaScriptReturningResult(String javaScript) async {
    scripts.add(javaScript);
    if (javaScript.startsWith('window.LuoboMapPickerV1.init(') &&
        !initialized.isCompleted) {
      initialized.complete();
    }
    return '{"accepted":true}';
  }
}

final class _HostDelegate extends PlatformNavigationDelegate {
  _HostDelegate(super.params) : super.implementation();
  NavigationRequestCallback? navigation;
  PageEventCallback? started, finished;
  HttpResponseErrorCallback? httpError;

  @override
  Future<void> setOnNavigationRequest(
    NavigationRequestCallback callback,
  ) async {
    navigation = callback;
  }

  @override
  Future<void> setOnPageStarted(PageEventCallback callback) async {
    started = callback;
  }

  @override
  Future<void> setOnPageFinished(PageEventCallback callback) async {
    finished = callback;
  }

  @override
  Future<void> setOnHttpError(HttpResponseErrorCallback callback) async {
    httpError = callback;
  }

  @override
  Future<void> setOnWebResourceError(WebResourceErrorCallback callback) async {}
}
