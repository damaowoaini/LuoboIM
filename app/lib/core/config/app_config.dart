import '../error/app_failure.dart';

abstract final class Brand {
  static const name = '萝卜对话';
  static const applicationId = 'com.luoboduihua.imapp';
}

/// Only build-time configuration chooses bootstrap services. Credentials are
/// never sent to a configuration source.
final class AppConfig {
  const AppConfig({
    this.routeSources = const [],
    this.fallbackRoutes = const [],
    this.privacyUrl = '',
    this.termsUrl = '',
  });

  factory AppConfig.fromEnvironment() => AppConfig(
    routeSources: _urls(const String.fromEnvironment('ROUTE_CONFIG_URLS')),
    fallbackRoutes: _urls(const String.fromEnvironment('ROUTE_URLS')),
    privacyUrl: const String.fromEnvironment('PRIVACY_URL'),
    termsUrl: const String.fromEnvironment('TERMS_URL'),
  );

  final List<Uri> routeSources;
  final List<Uri> fallbackRoutes;
  final String privacyUrl;
  final String termsUrl;

  bool get hasBootstrap => routeSources.isNotEmpty || fallbackRoutes.isNotEmpty;
  bool get hasPolicies => _isHttps(privacyUrl) && _isHttps(termsUrl);

  static bool _isHttps(String raw) {
    final uri = Uri.tryParse(raw);
    return uri != null &&
        uri.scheme == 'https' &&
        uri.host.isNotEmpty &&
        uri.userInfo.isEmpty;
  }

  static List<Uri> _urls(String raw) => raw
      .split(',')
      .map((s) => s.trim())
      .where((s) => s.isNotEmpty)
      .map(validateHttps)
      .toList(growable: false);
}

Uri validateHttps(String raw) {
  final uri = Uri.tryParse(raw.trim());
  if (uri == null ||
      uri.scheme != 'https' ||
      uri.host.isEmpty ||
      uri.userInfo.isNotEmpty ||
      uri.hasFragment) {
    throw const AppFailure(FailureKind.configuration);
  }
  return uri;
}
