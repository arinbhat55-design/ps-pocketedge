import 'package:flutter_secure_storage/flutter_secure_storage.dart';

/// Persists the JWT session token.
///
/// Note: on the web target, flutter_secure_storage falls back to browser
/// storage without OS-keychain-level protection — a known, documented
/// limitation for that platform rather than a silently accepted gap.
class AuthStorage {
  static const _tokenKey = 'auth_token';
  final FlutterSecureStorage _storage;

  AuthStorage({FlutterSecureStorage? storage})
    : _storage = storage ?? const FlutterSecureStorage();

  Future<String?> readToken() => _storage.read(key: _tokenKey);

  Future<void> writeToken(String token) =>
      _storage.write(key: _tokenKey, value: token);

  Future<void> clearToken() => _storage.delete(key: _tokenKey);
}
