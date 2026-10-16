import 'package:shared_preferences/shared_preferences.dart';

abstract interface class ConsentStore {
  Future<bool> read();
  Future<void> accept();
}

final class PreferencesConsentStore implements ConsentStore {
  PreferencesConsentStore(this._preferences);
  final SharedPreferencesAsync _preferences;
  static const _key = 'luobo.privacy.v1';
  @override
  Future<bool> read() async => await _preferences.getBool(_key) ?? false;
  @override
  Future<void> accept() => _preferences.setBool(_key, true);
}
