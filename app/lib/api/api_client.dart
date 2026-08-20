import 'dart:convert';

import 'package:http/http.dart' as http;

import '../models/backup.dart';
import '../models/server.dart';
import '../models/stack.dart';

class ApiException implements Exception {
  final int statusCode;
  final String message;
  ApiException(this.statusCode, this.message);

  @override
  String toString() => 'ApiException($statusCode): $message';
}

class EnrollmentToken {
  final String token;
  final DateTime expiresAt;
  final String installHint;

  const EnrollmentToken({
    required this.token,
    required this.expiresAt,
    required this.installHint,
  });

  factory EnrollmentToken.fromJson(Map<String, dynamic> json) {
    return EnrollmentToken(
      token: json['token'] as String,
      expiresAt: DateTime.parse(json['expiresAt'] as String),
      installHint: json['installHint'] as String,
    );
  }
}

/// Thin REST client for the control plane's JSON API.
///
/// [authToken] is attached as `Authorization: Bearer <token>` on every
/// request once set; it's mutable (rather than passed per-call) so the app
/// can update it in one place after login/logout.
class ApiClient {
  final String baseUrl;
  final http.Client _http;
  String? authToken;

  ApiClient({required this.baseUrl, http.Client? httpClient, this.authToken})
      : _http = httpClient ?? http.Client();

  Map<String, String> get _headers => {
        'Content-Type': 'application/json',
        if (authToken != null) 'Authorization': 'Bearer $authToken',
      };

  Future<String> login(String email, String password) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/auth/login'),
      headers: {'Content-Type': 'application/json'},
      body: jsonEncode({'email': email, 'password': password}),
    );
    if (response.statusCode != 200) {
      throw ApiException(response.statusCode, response.body);
    }
    final decoded = jsonDecode(response.body) as Map<String, dynamic>;
    return decoded['token'] as String;
  }

  Future<List<Server>> listServers() async {
    final response =
        await _http.get(Uri.parse('$baseUrl/api/servers'), headers: _headers);
    if (response.statusCode != 200) {
      throw ApiException(response.statusCode, response.body);
    }
    final decoded = jsonDecode(response.body) as List<dynamic>;
    return decoded
        .map((e) => Server.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  Future<EnrollmentToken> createEnrollmentToken() async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/servers/enroll-token'),
      headers: _headers,
    );
    if (response.statusCode != 200) {
      throw ApiException(response.statusCode, response.body);
    }
    return EnrollmentToken.fromJson(
        jsonDecode(response.body) as Map<String, dynamic>);
  }

  Future<List<StackSummary>> listStacks() async {
    final response =
        await _http.get(Uri.parse('$baseUrl/api/stacks'), headers: _headers);
    if (response.statusCode != 200) {
      throw ApiException(response.statusCode, response.body);
    }
    final decoded = jsonDecode(response.body) as List<dynamic>;
    return decoded
        .map((e) => StackSummary.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  Future<String> createDeployment({
    required String stackId,
    required String serverId,
    Map<String, String> env = const {},
  }) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/deployments'),
      headers: _headers,
      body: jsonEncode({'stackId': stackId, 'serverId': serverId, 'env': env}),
    );
    final decoded = jsonDecode(response.body) as Map<String, dynamic>;
    if (response.statusCode != 202) {
      throw ApiException(
        response.statusCode,
        decoded['error'] as String? ?? response.body,
      );
    }
    return decoded['deploymentId'] as String;
  }

  /// Re-applies an existing deployment (same deployment_id) to its server.
  /// Unlike [createDeployment], this exercises the agent's redeploy/recreate
  /// path: the agent finds containers already labeled with this
  /// deployment_id and replaces them, rather than creating a parallel set.
  Future<void> redeployDeployment(String deploymentId) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/deployments/$deploymentId/redeploy'),
      headers: _headers,
    );
    if (response.statusCode != 202) {
      final decoded = jsonDecode(response.body) as Map<String, dynamic>;
      throw ApiException(
        response.statusCode,
        decoded['error'] as String? ?? response.body,
      );
    }
  }

  Future<String> createBackup(String deploymentId) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/deployments/$deploymentId/backups'),
      headers: _headers,
    );
    if (response.statusCode != 202) {
      final decoded = jsonDecode(response.body) as Map<String, dynamic>;
      throw ApiException(
        response.statusCode,
        decoded['error'] as String? ?? response.body,
      );
    }
    final decoded = jsonDecode(response.body) as Map<String, dynamic>;
    return decoded['backupId'] as String;
  }

  Future<List<Backup>> listBackups(String deploymentId) async {
    final response = await _http.get(
      Uri.parse('$baseUrl/api/deployments/$deploymentId/backups'),
      headers: _headers,
    );
    if (response.statusCode != 200) {
      throw ApiException(response.statusCode, response.body);
    }
    final decoded = jsonDecode(response.body) as List<dynamic>;
    return decoded
        .map((e) => Backup.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  /// Restores backup's deployment from that snapshot. Always restores into
  /// the same deployment it was taken from — there's no "restore into a
  /// new deployment" yet. Progress after this call shows up in the
  /// deployment's normal live status stream (deploymentStreamUri), not a
  /// separate backup-status feed.
  Future<void> restoreBackup(String backupId) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/backups/$backupId/restore'),
      headers: _headers,
    );
    if (response.statusCode != 202) {
      final decoded = jsonDecode(response.body) as Map<String, dynamic>;
      throw ApiException(
        response.statusCode,
        decoded['error'] as String? ?? response.body,
      );
    }
  }

  /// WebSocket URL for a deployment's live status stream. The JWT travels
  /// as a `?token=` query param here rather than an Authorization header —
  /// browsers can't set custom headers on a WebSocket handshake, so the
  /// server accepts this one endpoint's auth that way; see the server's
  /// handleDeploymentStream doc comment.
  Uri deploymentStreamUri(String deploymentId) {
    final httpUri = Uri.parse(baseUrl);
    final wsScheme = httpUri.scheme == 'https' ? 'wss' : 'ws';
    return httpUri.replace(
      scheme: wsScheme,
      path: '/api/deployments/$deploymentId/stream',
      queryParameters: {'token': ?authToken},
    );
  }
}
