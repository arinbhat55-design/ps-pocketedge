import 'dart:async';

import 'package:flutter/material.dart';

import 'api/api_client.dart';
import 'api/auth_storage.dart';
import 'features/auth/login_screen.dart';
import 'features/shell/app_shell.dart';
import 'theme/app_theme.dart';

/// Control-plane REST API base URL. Override at build/run time with
/// `--dart-define=CONTROL_PLANE_URL=http://host:port`
const controlPlaneUrl = String.fromEnvironment(
  'CONTROL_PLANE_URL',
  defaultValue: 'http://localhost:8080',
);

void main() {
  runApp(const PSPocketEdgeApp());
}

class PSPocketEdgeApp extends StatelessWidget {
  const PSPocketEdgeApp({super.key});

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'PS-pocketEdge',
      theme: AppTheme.light(),
      darkTheme: AppTheme.dark(),
      themeMode: ThemeMode.dark,
      home: const SessionGate(),
    );
  }
}

/// Local control planes grant a session automatically. Remote ones use login.
class SessionGate extends StatefulWidget {
  const SessionGate({super.key});

  @override
  State<SessionGate> createState() => _SessionGateState();
}

class _SessionGateState extends State<SessionGate> {
  final _authStorage = AuthStorage();
  late final ApiClient _apiClient = ApiClient(baseUrl: controlPlaneUrl)
    ..onUnauthorized = _onUnauthorized
    ..onTokenRenewed = _onTokenRenewed;

  bool _starting = true;
  String? _token;
  bool _localMode = false;
  String? _loginNotice;
  Timer? _localRefreshTimer;
  bool _recovering = false;

  /// Local access can only work when the dashboard talks to a control plane
  /// on this computer.
  static final bool _localAccessPossible = const {
    'localhost',
    '127.0.0.1',
    '::1',
  }.contains(Uri.parse(controlPlaneUrl).host);

  @override
  void initState() {
    super.initState();
    unawaited(_start());
  }

  @override
  void dispose() {
    _localRefreshTimer?.cancel();
    super.dispose();
  }

  Future<void> _start() async {
    final local = await _requestLocalSession();
    final token = local ?? await _authStorage.readToken();
    if (!mounted) return;
    _show(token, local: local != null);
  }

  Future<String?> _requestLocalSession() async {
    if (!_localAccessPossible) return null;
    try {
      return await _apiClient.localSession();
    } catch (_) {
      return null;
    }
  }

  void _show(String? token, {required bool local, String? notice}) {
    _apiClient.authToken = token;
    _localRefreshTimer?.cancel();
    if (token != null && local) {
      // Renew well before the 24h expiry; a 401 also renews (_recover).
      _localRefreshTimer = Timer.periodic(const Duration(hours: 12), (_) async {
        final renewed = await _requestLocalSession();
        if (renewed != null && mounted && _localMode) {
          _apiClient.authToken = renewed;
        }
      });
    }
    setState(() {
      _starting = false;
      _token = token;
      _localMode = token != null && local;
      _loginNotice = notice;
    });
  }

  /// A request was rejected with 401: the token expired, local access was
  /// turned off, or the account was removed. Renew the local session when
  /// that is still allowed, otherwise return to the login screen.
  void _onUnauthorized() {
    if (_token == null || _recovering) return;
    _recovering = true;
    unawaited(_recover().whenComplete(() => _recovering = false));
  }

  Future<void> _recover() async {
    final rejected = _apiClient.authToken;
    final local = await _requestLocalSession();
    // Ignore if the user logged in or out while this was in flight.
    if (!mounted || _apiClient.authToken != rejected) return;
    if (local != null) {
      _show(local, local: true);
      return;
    }
    unawaited(_authStorage.clearToken());
    _show(
      null,
      local: false,
      notice: 'Your session ended. Sign in to continue.',
    );
  }

  /// The server replaced the session token (a password change ends every
  /// earlier session); keep using and storing the new one.
  void _onTokenRenewed(String token) {
    if (!_localMode) unawaited(_authStorage.writeToken(token));
    setState(() => _token = token);
  }

  void _onLoggedIn(String token) {
    unawaited(_authStorage.writeToken(token));
    _show(token, local: false);
  }

  void _onLogout() {
    unawaited(_authStorage.clearToken());
    _show(null, local: false);
  }

  void _onAccessModeChanged(bool requireLogin) {
    unawaited(_authStorage.clearToken());
    if (requireLogin) {
      _show(
        null,
        local: false,
        notice: 'Login is now required on this computer.',
      );
      return;
    }
    _apiClient.authToken = null;
    setState(() => _starting = true);
    unawaited(_start());
  }

  @override
  Widget build(BuildContext context) {
    if (_starting) {
      return const Scaffold(body: Center(child: CircularProgressIndicator()));
    }
    if (_token != null) {
      return AppShell(
        // Switching between local and signed-in sessions changes who the
        // user is, so start the shell fresh.
        key: ValueKey(_localMode),
        apiClient: _apiClient,
        onLogout: _onLogout,
        onAccessModeChanged: _onAccessModeChanged,
        localMode: _localMode,
      );
    }
    return LoginScreen(
      apiClient: _apiClient,
      onLoggedIn: _onLoggedIn,
      notice: _loginNotice,
      onLocalSession: _localAccessPossible
          ? (token) => _show(token, local: true)
          : null,
    );
  }
}
