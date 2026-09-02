import 'dart:async';

import 'package:flutter/material.dart';

import 'api/api_client.dart';
import 'api/auth_storage.dart';
import 'features/auth/login_screen.dart';
import 'features/shell/app_shell.dart';

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
      title: 'PSpocketEdge',
      theme: ThemeData(
        colorScheme: ColorScheme.fromSeed(seedColor: Colors.teal),
      ),
      home: const SessionGate(),
    );
  }
}

/// Shows LoginScreen until a valid session token is available (freshly
/// logged in, or restored from secure storage), then ServerListScreen.
class SessionGate extends StatefulWidget {
  const SessionGate({super.key});

  @override
  State<SessionGate> createState() => _SessionGateState();
}

class _SessionGateState extends State<SessionGate> {
  final _authStorage = AuthStorage();
  late final ApiClient _apiClient = ApiClient(baseUrl: controlPlaneUrl);

  Future<String?>? _restoredTokenFuture;
  String? _sessionToken;

  @override
  void initState() {
    super.initState();
    _restoredTokenFuture = _authStorage.readToken();
  }

  void _onLoggedIn(String token) {
    _apiClient.authToken = token;
    unawaited(_authStorage.writeToken(token));
    setState(() => _sessionToken = token);
  }

  void _onLogout() {
    _apiClient.authToken = null;
    unawaited(_authStorage.clearToken());
    setState(() => _sessionToken = null);
  }

  @override
  Widget build(BuildContext context) {
    if (_sessionToken != null) {
      return AppShell(apiClient: _apiClient, onLogout: _onLogout);
    }

    return FutureBuilder<String?>(
      future: _restoredTokenFuture,
      builder: (context, snapshot) {
        if (snapshot.connectionState != ConnectionState.done) {
          return const Scaffold(
            body: Center(child: CircularProgressIndicator()),
          );
        }
        final restored = snapshot.data;
        if (restored != null) {
          _apiClient.authToken = restored;
          return AppShell(apiClient: _apiClient, onLogout: _onLogout);
        }
        return LoginScreen(apiClient: _apiClient, onLoggedIn: _onLoggedIn);
      },
    );
  }
}
