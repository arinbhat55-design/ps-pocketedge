import 'dart:convert';

import 'package:http/http.dart' as http;

import '../models/backup.dart';
import '../models/container.dart';
import '../models/server.dart';
import '../models/server_metrics.dart';
import '../models/stack.dart';
import '../models/user.dart';

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

  Future<AppUser> getMe() async {
    final response =
        await _http.get(Uri.parse('$baseUrl/api/auth/me'), headers: _headers);
    if (response.statusCode != 200) {
      throw ApiException(response.statusCode, response.body);
    }
    return AppUser.fromJson(jsonDecode(response.body) as Map<String, dynamic>);
  }

  Future<void> changePassword({
    required String currentPassword,
    required String newPassword,
  }) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/auth/change-password'),
      headers: _headers,
      body: jsonEncode({
        'currentPassword': currentPassword,
        'newPassword': newPassword,
      }),
    );
    if (response.statusCode != 204) {
      throw ApiException(response.statusCode, response.body);
    }
  }

  Future<List<AppUser>> listUsers() async {
    final response =
        await _http.get(Uri.parse('$baseUrl/api/users'), headers: _headers);
    if (response.statusCode != 200) {
      throw ApiException(response.statusCode, response.body);
    }
    final decoded = jsonDecode(response.body) as List<dynamic>;
    return decoded
        .map((e) => AppUser.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  Future<void> createUser({
    required String email,
    required String password,
    required String role,
  }) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/users'),
      headers: _headers,
      body: jsonEncode({'email': email, 'password': password, 'role': role}),
    );
    if (response.statusCode != 201) {
      throw ApiException(response.statusCode, response.body);
    }
  }

  Future<void> updateUserRole(String userId, String role) async {
    final response = await _http.patch(
      Uri.parse('$baseUrl/api/users/$userId/role'),
      headers: _headers,
      body: jsonEncode({'role': role}),
    );
    if (response.statusCode != 204) {
      throw ApiException(response.statusCode, response.body);
    }
  }

  Future<void> resetUserPassword(String userId, String password) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/users/$userId/reset-password'),
      headers: _headers,
      body: jsonEncode({'password': password}),
    );
    if (response.statusCode != 204) {
      throw ApiException(response.statusCode, response.body);
    }
  }

  Future<void> deleteUser(String userId) async {
    final response = await _http.delete(
      Uri.parse('$baseUrl/api/users/$userId'),
      headers: _headers,
    );
    if (response.statusCode != 204) {
      throw ApiException(response.statusCode, response.body);
    }
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

  Future<ServerDetail> getServerDetail(String serverId) async {
    final response = await _http.get(
      Uri.parse('$baseUrl/api/servers/$serverId'),
      headers: _headers,
    );
    if (response.statusCode != 200) {
      throw ApiException(response.statusCode, response.body);
    }
    return ServerDetail.fromJson(
        jsonDecode(response.body) as Map<String, dynamic>);
  }

  /// [since] is a Go duration string (e.g. '1h', '30m'); defaults to the
  /// server's own default window (1h) if omitted.
  Future<List<MetricSample>> getServerMetrics(String serverId,
      {String? since}) async {
    final uri = Uri.parse('$baseUrl/api/servers/$serverId/metrics').replace(
      queryParameters: since == null ? null : {'since': since},
    );
    final response = await _http.get(uri, headers: _headers);
    if (response.statusCode != 200) {
      throw ApiException(response.statusCode, response.body);
    }
    final decoded = jsonDecode(response.body) as List<dynamic>;
    return decoded
        .map((e) => MetricSample.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  /// WebSocket URL for a server's live resource/container status stream —
  /// same `?token=` query-param auth pattern as [deploymentStreamUri].
  Uri serverStreamUri(String serverId) {
    final httpUri = Uri.parse(baseUrl);
    final wsScheme = httpUri.scheme == 'https' ? 'wss' : 'ws';
    return httpUri.replace(
      scheme: wsScheme,
      path: '/api/servers/$serverId/stream',
      queryParameters: {'token': ?authToken},
    );
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

  /// Fleet-wide container inventory, optionally narrowed by any of these
  /// filters (all server-side, since the list spans the whole fleet).
  Future<List<FleetContainer>> listContainers({
    String? name,
    String? image,
    String? serverId,
    String? status,
    String? ownerId,
    String? environment,
    List<String>? tags,
  }) async {
    final uri = Uri.parse('$baseUrl/api/containers').replace(
      queryParameters: {
        if (name != null && name.isNotEmpty) 'name': name,
        if (image != null && image.isNotEmpty) 'image': image,
        if (serverId != null && serverId.isNotEmpty) 'serverId': serverId,
        if (status != null && status.isNotEmpty) 'status': status,
        if (ownerId != null && ownerId.isNotEmpty) 'ownerId': ownerId,
        if (environment != null && environment.isNotEmpty)
          'environment': environment,
        if (tags != null && tags.isNotEmpty) 'tag': tags,
      },
    );
    final response = await _http.get(uri, headers: _headers);
    if (response.statusCode != 200) {
      throw ApiException(response.statusCode, response.body);
    }
    final decoded = jsonDecode(response.body) as List<dynamic>;
    return decoded
        .map((e) => FleetContainer.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  /// Fetches the expensive, on-demand fields (env vars, restart policy,
  /// health) for one container by asking the owning agent to run a live
  /// ContainerInspect — not included in the cheap [FleetContainer]/
  /// [ContainerInfo] fields returned by the heartbeat-fed endpoints.
  Future<ContainerDetail> inspectContainer(
      String serverId, String containerId) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/servers/$serverId/containers/$containerId/inspect'),
      headers: _headers,
    );
    if (response.statusCode != 200) {
      final decoded = jsonDecode(response.body) as Map<String, dynamic>;
      throw ApiException(
        response.statusCode,
        decoded['error'] as String? ?? response.body,
      );
    }
    return ContainerDetail.fromJson(
        jsonDecode(response.body) as Map<String, dynamic>);
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
