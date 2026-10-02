import 'dart:convert';

import 'package:http/http.dart' as http;

import '../models/audit_event.dart';
import '../models/backup.dart';
import '../models/build.dart';
import '../models/compose_file.dart';
import '../models/container.dart';
import '../models/database.dart';
import '../models/deployment.dart';
import '../models/deployment_preview.dart';
import '../models/deployment_request.dart';
import '../models/deployment_revision.dart';
import '../models/drift_report.dart';
import '../models/environment_policy.dart';
import '../models/git_report.dart';
import '../models/git_repository.dart';
import '../models/env_var_group.dart';
import '../models/image.dart';
import '../models/log_line.dart';
import '../models/network.dart';
import '../models/resource_insights.dart';
import '../models/schedule.dart';
import '../models/server.dart';
import '../models/server_metrics.dart';
import '../models/stack.dart';
import '../models/user.dart';
import '../models/volume.dart';

class ApiException implements Exception {
  final int statusCode;
  final String message;
  ApiException(this.statusCode, this.message);

  @override
  String toString() => 'ApiException($statusCode): $message';
}

/// An action refused because its environment only allows changes during a
/// maintenance window. The caller can retry with
/// [GateOptions.scheduleForMaintenanceWindow] (runs at [nextWindow]) or,
/// when [canOverride], [GateOptions.overrideMaintenanceWindow].
class MaintenanceWindowException extends ApiException {
  final DateTime? nextWindow;
  final bool canOverride;

  MaintenanceWindowException(
    super.statusCode,
    super.message, {
    this.nextWindow,
    this.canOverride = false,
  });
}

/// Throws the right [ApiException] for a failed response: the JSON
/// `error` field when there is one, and a [MaintenanceWindowException]
/// for a maintenance-window refusal.
Never throwApiError(http.Response response) {
  Map<String, dynamic>? body;
  try {
    final decoded = jsonDecode(response.body);
    if (decoded is Map<String, dynamic>) body = decoded;
  } catch (_) {}
  final message =
      (body?['error'] as String?) ??
      (body?['errors'] is List
          ? (body!['errors'] as List).join('; ')
          : response.body.trim());
  if (body?['outsideMaintenanceWindow'] == true) {
    throw MaintenanceWindowException(
      response.statusCode,
      message,
      nextWindow: body!['nextWindow'] == null
          ? null
          : DateTime.parse(body['nextWindow'] as String),
      canOverride: body['canOverride'] as bool? ?? false,
    );
  }
  throw ApiException(response.statusCode, message);
}

/// A rotation where the database accepted the new password but the
/// control plane failed to store it. [newPassword] is the only copy — the
/// user must save it.
class UnsavedCredentialException extends ApiException {
  final String newPassword;

  UnsavedCredentialException(super.statusCode, super.message, this.newPassword);
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
/// Reports a 401 on a request that carried a token, so the app can renew or
/// end the session instead of every screen showing an error.
class _UnauthorizedWatcher extends http.BaseClient {
  final http.Client _inner;
  final void Function() _onUnauthorized;

  _UnauthorizedWatcher(this._inner, this._onUnauthorized);

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    final response = await _inner.send(request);
    if (response.statusCode == 401 &&
        request.headers.containsKey('Authorization')) {
      _onUnauthorized();
    }
    return response;
  }

  @override
  void close() => _inner.close();
}

class ApiClient {
  final String baseUrl;
  late final http.Client _http;
  String? authToken;

  /// Called when the server rejects [authToken] with 401.
  void Function()? onUnauthorized;

  /// Called when the server replaces [authToken] (after a password change,
  /// which ends every earlier session), so the app can persist the new one.
  void Function(String token)? onTokenRenewed;

  ApiClient({required this.baseUrl, http.Client? httpClient, this.authToken}) {
    _http = _UnauthorizedWatcher(
      httpClient ?? http.Client(),
      () => onUnauthorized?.call(),
    );
  }

  Map<String, String> get _headers => {
    'Content-Type': 'application/json',
    if (authToken != null) 'Authorization': 'Bearer $authToken',
  };

  Future<List<Map<String, dynamic>>> listKubernetesClusters() async {
    final response = await _http.get(
      Uri.parse('$baseUrl/api/kubernetes/clusters'),
      headers: _headers,
    );
    if (response.statusCode != 200) throwApiError(response);
    return (jsonDecode(response.body) as List).cast<Map<String, dynamic>>();
  }

  Future<void> addKubernetesCluster(String name, String kubeconfig) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/kubernetes/clusters'),
      headers: _headers,
      body: jsonEncode({'name': name, 'kubeconfig': kubeconfig}),
    );
    if (response.statusCode != 201) throwApiError(response);
  }

  Future<void> createLocalKubernetesCluster(String name) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/kubernetes/local-clusters'),
      headers: _headers,
      body: jsonEncode({'name': name}),
    );
    if (response.statusCode != 201) throwApiError(response);
  }

  Future<void> deleteLocalKubernetesCluster(String id) async {
    final response = await _http.delete(
      Uri.parse(
        '$baseUrl/api/kubernetes/local-clusters/${Uri.encodeComponent(id)}',
      ),
      headers: _headers,
    );
    if (response.statusCode != 204) throwApiError(response);
  }

  Future<void> removeKubernetesCluster(String id) async {
    final response = await _http.delete(
      Uri.parse('$baseUrl/api/kubernetes/clusters/${Uri.encodeComponent(id)}'),
      headers: _headers,
    );
    if (response.statusCode != 204) throwApiError(response);
  }

  Future<Map<String, dynamic>> kubernetesOverview(
    String id, {
    String? namespace,
  }) async {
    final uri =
        Uri.parse(
          '$baseUrl/api/kubernetes/clusters/${Uri.encodeComponent(id)}/overview',
        ).replace(
          queryParameters: namespace == null ? null : {'namespace': namespace},
        );
    final response = await _http.get(uri, headers: _headers);
    if (response.statusCode != 200) throwApiError(response);
    return jsonDecode(response.body) as Map<String, dynamic>;
  }

  Future<String> kubernetesPodLogs(
    String id,
    String namespace,
    String pod,
  ) async {
    final response = await _http.get(
      Uri.parse(
        '$baseUrl/api/kubernetes/clusters/${Uri.encodeComponent(id)}/pods/'
        '${Uri.encodeComponent(namespace)}/${Uri.encodeComponent(pod)}/logs',
      ),
      headers: _headers,
    );
    if (response.statusCode != 200) throwApiError(response);
    return response.body;
  }

  Future<Map<String, dynamic>> deployKubernetesWorkload(
    String id,
    Map<String, dynamic> spec, {
    bool dryRun = false,
  }) async {
    final uri = Uri.parse(
      '$baseUrl/api/kubernetes/clusters/${Uri.encodeComponent(id)}/workloads',
    ).replace(queryParameters: dryRun ? {'dryRun': 'true'} : null);
    final response = await _http.post(
      uri,
      headers: _headers,
      body: jsonEncode(spec),
    );
    if (response.statusCode != (dryRun ? 200 : 201)) throwApiError(response);
    return jsonDecode(response.body) as Map<String, dynamic>;
  }

  Future<Map<String, dynamic>> updateKubernetesWorkload(
    String id,
    String namespace,
    String name,
    Map<String, dynamic> spec, {
    bool dryRun = false,
  }) async {
    final uri = Uri.parse(
      '$baseUrl/api/kubernetes/clusters/${Uri.encodeComponent(id)}/workloads/'
      '${Uri.encodeComponent(namespace)}/${Uri.encodeComponent(name)}',
    ).replace(queryParameters: dryRun ? {'dryRun': 'true'} : null);
    final response = await _http.put(
      uri,
      headers: _headers,
      body: jsonEncode(spec),
    );
    if (response.statusCode != 200) throwApiError(response);
    return jsonDecode(response.body) as Map<String, dynamic>;
  }

  Future<Map<String, dynamic>> deployKubernetesOllama(
    String id,
    Map<String, dynamic> spec, {
    bool dryRun = false,
  }) async {
    final uri = Uri.parse(
      '$baseUrl/api/kubernetes/clusters/${Uri.encodeComponent(id)}/ai/ollama',
    ).replace(queryParameters: dryRun ? {'dryRun': 'true'} : null);
    final response = await _http.post(
      uri,
      headers: _headers,
      body: jsonEncode(spec),
    );
    if (response.statusCode != (dryRun ? 200 : 201)) throwApiError(response);
    return jsonDecode(response.body) as Map<String, dynamic>;
  }

  Future<List<Map<String, dynamic>>> kubernetesRevisions(
    String id,
    String namespace,
    String name,
  ) async {
    final path =
        '$baseUrl/api/kubernetes/clusters/${Uri.encodeComponent(id)}/workloads/'
        '${Uri.encodeComponent(namespace)}/${Uri.encodeComponent(name)}/revisions';
    final response = await _http.get(Uri.parse(path), headers: _headers);
    if (response.statusCode != 200) throwApiError(response);
    return (jsonDecode(response.body) as List).cast<Map<String, dynamic>>();
  }

  Future<void> rollbackKubernetesWorkload(
    String id,
    String namespace,
    String name,
    int revision, {
    bool dryRun = false,
  }) async {
    final uri = Uri.parse(
      '$baseUrl/api/kubernetes/clusters/${Uri.encodeComponent(id)}/workloads/'
      '${Uri.encodeComponent(namespace)}/${Uri.encodeComponent(name)}/rollback',
    ).replace(queryParameters: dryRun ? {'dryRun': 'true'} : null);
    final response = await _http.post(
      uri,
      headers: _headers,
      body: jsonEncode({'revision': revision}),
    );
    if (response.statusCode != 200) throwApiError(response);
  }

  Future<void> deleteKubernetesWorkload(
    String id,
    String namespace,
    String name,
  ) async {
    final response = await _http.delete(
      Uri.parse(
        '$baseUrl/api/kubernetes/clusters/${Uri.encodeComponent(id)}/workloads/'
        '${Uri.encodeComponent(namespace)}/${Uri.encodeComponent(name)}',
      ),
      headers: _headers,
    );
    if (response.statusCode != 204) throwApiError(response);
  }

  Future<List<Map<String, dynamic>>> kubernetesHelmReleases(
    String id,
    String namespace,
  ) async {
    final uri = Uri.parse(
      '$baseUrl/api/kubernetes/clusters/${Uri.encodeComponent(id)}/helm',
    ).replace(queryParameters: {'namespace': namespace});
    final response = await _http.get(uri, headers: _headers);
    if (response.statusCode != 200) throwApiError(response);
    return (jsonDecode(response.body) as List).cast<Map<String, dynamic>>();
  }

  Future<Map<String, dynamic>> applyKubernetesHelmChart(
    String id,
    Map<String, dynamic> spec, {
    bool dryRun = false,
  }) async {
    final uri = Uri.parse(
      '$baseUrl/api/kubernetes/clusters/${Uri.encodeComponent(id)}/helm',
    ).replace(queryParameters: dryRun ? {'dryRun': 'true'} : null);
    final response = await _http.post(
      uri,
      headers: _headers,
      body: jsonEncode(spec),
    );
    if (response.statusCode != 200) throwApiError(response);
    return jsonDecode(response.body) as Map<String, dynamic>;
  }

  Future<List<Map<String, dynamic>>> kubernetesHelmHistory(
    String id,
    String namespace,
    String name,
  ) async {
    final uri = Uri.parse(
      '$baseUrl/api/kubernetes/clusters/${Uri.encodeComponent(id)}/helm/'
      '${Uri.encodeComponent(namespace)}/${Uri.encodeComponent(name)}/history',
    );
    final response = await _http.get(uri, headers: _headers);
    if (response.statusCode != 200) throwApiError(response);
    return (jsonDecode(response.body) as List).cast<Map<String, dynamic>>();
  }

  Future<void> rollbackKubernetesHelm(
    String id,
    String namespace,
    String name,
    int revision,
  ) async {
    final uri = Uri.parse(
      '$baseUrl/api/kubernetes/clusters/${Uri.encodeComponent(id)}/helm/'
      '${Uri.encodeComponent(namespace)}/${Uri.encodeComponent(name)}/rollback',
    );
    final response = await _http.post(
      uri,
      headers: _headers,
      body: jsonEncode({'revision': revision}),
    );
    if (response.statusCode != 204) throwApiError(response);
  }

  Future<void> uninstallKubernetesHelm(
    String id,
    String namespace,
    String name,
  ) async {
    final uri = Uri.parse(
      '$baseUrl/api/kubernetes/clusters/${Uri.encodeComponent(id)}/helm/'
      '${Uri.encodeComponent(namespace)}/${Uri.encodeComponent(name)}',
    );
    final response = await _http.delete(uri, headers: _headers);
    if (response.statusCode != 204) throwApiError(response);
  }

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

  /// Starts a session only when the control plane accepts local access.
  Future<String> localSession() async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/auth/local-session'),
    );
    if (response.statusCode != 200) throwApiError(response);
    return (jsonDecode(response.body) as Map<String, dynamic>)['token']
        as String;
  }

  Future<AppUser> getMe() async {
    final response = await _http.get(
      Uri.parse('$baseUrl/api/auth/me'),
      headers: _headers,
    );
    if (response.statusCode != 200) {
      throw ApiException(response.statusCode, response.body);
    }
    return AppUser.fromJson(jsonDecode(response.body) as Map<String, dynamic>);
  }

  Future<Map<String, dynamic>> getAccessSettings() async {
    final response = await _http.get(
      Uri.parse('$baseUrl/api/settings/access'),
      headers: _headers,
    );
    if (response.statusCode != 200) throwApiError(response);
    return jsonDecode(response.body) as Map<String, dynamic>;
  }

  Future<Map<String, dynamic>> updateAccessSettings({
    required bool requireLocalLogin,
    String? password,
  }) async {
    final response = await _http.put(
      Uri.parse('$baseUrl/api/settings/access'),
      headers: _headers,
      body: jsonEncode({
        'requireLocalLogin': requireLocalLogin,
        'password': ?password,
      }),
    );
    if (response.statusCode != 200) throwApiError(response);
    return jsonDecode(response.body) as Map<String, dynamic>;
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
    if (response.statusCode == 204) return;
    if (response.statusCode != 200) {
      throw ApiException(response.statusCode, response.body);
    }
    final token =
        (jsonDecode(response.body) as Map<String, dynamic>)['token'] as String?;
    if (token != null) {
      authToken = token;
      onTokenRenewed?.call(token);
    }
  }

  Future<List<AppUser>> listUsers() async {
    final response = await _http.get(
      Uri.parse('$baseUrl/api/users'),
      headers: _headers,
    );
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

  /// Retires a server that's no longer reporting (admin only). Throws an
  /// [ApiException] with the control plane's reason on failure — 409 while
  /// the server's agent is still connected.
  Future<void> removeServer(String serverId) async {
    final response = await _http.delete(
      Uri.parse('$baseUrl/api/servers/$serverId'),
      headers: _headers,
    );
    if (response.statusCode == 204) return;
    var message = response.body.trim();
    try {
      final decoded = jsonDecode(response.body);
      if (decoded is Map && decoded['error'] is String) {
        message = decoded['error'] as String;
      }
    } catch (_) {}
    throw ApiException(response.statusCode, message);
  }

  Future<List<Server>> listServers() async {
    final response = await _http.get(
      Uri.parse('$baseUrl/api/servers'),
      headers: _headers,
    );
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
      jsonDecode(response.body) as Map<String, dynamic>,
    );
  }

  /// [since] is a Go duration string (e.g. '1h', '30m'); defaults to the
  /// server's own default window (1h) if omitted.
  Future<List<MetricSample>> getServerMetrics(
    String serverId, {
    String? since,
  }) async {
    final uri = Uri.parse(
      '$baseUrl/api/servers/$serverId/metrics',
    ).replace(queryParameters: since == null ? null : {'since': since});
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
      jsonDecode(response.body) as Map<String, dynamic>,
    );
  }

  Future<List<StackSummary>> listStacks() async {
    final response = await _http.get(
      Uri.parse('$baseUrl/api/stacks'),
      headers: _headers,
    );
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
    if (response.statusCode != 202) throwApiError(response);
    final decoded = jsonDecode(response.body) as Map<String, dynamic>;
    return decoded['deploymentId'] as String;
  }

  /// Deploys a user-authored Compose file (as opposed to [createDeployment],
  /// which deploys from the curated stacks catalog). The environment's
  /// policy decides whether it runs now, waits for approval, or waits for
  /// a maintenance window — see [DeploymentActionOutcome.status]. Throws a
  /// [MaintenanceWindowException] when outside a window and [gate] says
  /// neither to schedule nor override.
  Future<DeploymentActionOutcome> createComposeDeployment({
    required String composeFileId,
    required String serverId,
    Map<String, String> env = const {},
    DeploymentMetadata metadata = const DeploymentMetadata(),
    Map<String, int> scales = const {},
    GateOptions gate = GateOptions.none,
  }) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/deployments'),
      headers: _headers,
      body: jsonEncode({
        'composeFileId': composeFileId,
        'serverId': serverId,
        'env': env,
        ...metadata.toJson(),
        if (scales.isNotEmpty) 'scales': scales,
        ...gate.toJson(),
      }),
    );
    if (response.statusCode != 202) throwApiError(response);
    return DeploymentActionOutcome.fromJson(
      jsonDecode(response.body) as Map<String, dynamic>,
    );
  }

  /// "Deployment history": every deployment, newest first, optionally
  /// filtered.
  Future<List<DeploymentSummary>> listDeployments({
    String? environment,
    String? serverId,
    String? composeFileId,
    String? phase,
    String? search,
    bool includeRemoved = false,
  }) async {
    final response = await _http.get(
      Uri.parse('$baseUrl/api/deployments').replace(
        queryParameters: {
          if (environment != null && environment.isNotEmpty)
            'environment': environment,
          if (serverId != null && serverId.isNotEmpty) 'serverId': serverId,
          if (composeFileId != null && composeFileId.isNotEmpty)
            'composeFileId': composeFileId,
          if (phase != null && phase.isNotEmpty) 'phase': phase,
          if (search != null && search.isNotEmpty) 'q': search,
          if (includeRemoved) 'includeRemoved': 'true',
        },
      ),
      headers: _headers,
    );
    if (response.statusCode != 200) throwApiError(response);
    return (jsonDecode(response.body) as List<dynamic>)
        .map((e) => DeploymentSummary.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  /// Fetches a deployment's governance/rollout metadata — live
  /// status/progress comes from the deployment events stream instead.
  Future<Deployment> getDeployment(String deploymentId) async {
    return (await getDeploymentDetail(deploymentId)).deployment;
  }

  /// Fetches a deployment with its timeline, open requests, environment
  /// policy, and rollback target.
  Future<DeploymentDetail> getDeploymentDetail(String deploymentId) async {
    final response = await _http.get(
      Uri.parse('$baseUrl/api/deployments/$deploymentId'),
      headers: _headers,
    );
    if (response.statusCode != 200) throwApiError(response);
    return DeploymentDetail.fromJson(
      jsonDecode(response.body) as Map<String, dynamic>,
    );
  }

  /// Replaces a deployment's editable metadata — send the whole desired
  /// state, not a partial patch.
  Future<void> updateDeploymentMetadata(
    String deploymentId,
    DeploymentMetadata metadata,
  ) async {
    final response = await _http.patch(
      Uri.parse('$baseUrl/api/deployments/$deploymentId/metadata'),
      headers: _headers,
      body: jsonEncode(metadata.toJson()),
    );
    if (response.statusCode != 204) throwApiError(response);
  }

  /// Applies a stack-level lifecycle action to every container in a
  /// deployment — start/stop/restart fan out across the deployment's
  /// current containers (response has a `results` list, one per
  /// container, each `{serverId, containerId, success, error}`); remove
  /// tears the whole deployment down including its networks (response is
  /// `{success, error}`). Callers branch on [action] to interpret which
  /// shape came back — see DeploymentStatusScreen.
  Future<Map<String, dynamic>> deploymentAction(
    String deploymentId,
    String action,
  ) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/deployments/$deploymentId/action'),
      headers: _headers,
      body: jsonEncode({'action': action}),
    );
    if (response.statusCode != 200) throwApiError(response);
    return jsonDecode(response.body) as Map<String, dynamic>;
  }

  Future<DeploymentActionOutcome> _postAction(
    String path,
    Map<String, dynamic> body,
  ) async {
    final response = await _http.post(
      Uri.parse('$baseUrl$path'),
      headers: _headers,
      body: jsonEncode(body),
    );
    if (response.statusCode != 200 && response.statusCode != 202) {
      throwApiError(response);
    }
    return DeploymentActionOutcome.fromJson(
      jsonDecode(response.body) as Map<String, dynamic>,
    );
  }

  /// Re-applies an existing deployment (same deployment_id) to its server —
  /// recreating its containers, or a rolling update with automatic
  /// rollback when the deployment's strategy is "rolling".
  Future<DeploymentActionOutcome> redeployDeployment(
    String deploymentId, {
    GateOptions gate = GateOptions.none,
  }) => _postAction('/api/deployments/$deploymentId/redeploy', gate.toJson());

  /// Redeploys one named service within a deployment — pulls its image and
  /// recreates just that service's containers.
  Future<DeploymentActionOutcome> redeployService(
    String deploymentId,
    String serviceName, {
    GateOptions gate = GateOptions.none,
  }) => _postAction(
    '/api/deployments/$deploymentId/services/${Uri.encodeComponent(serviceName)}/redeploy',
    gate.toJson(),
  );

  /// "Scale supported services": sets how many containers one service runs.
  Future<DeploymentActionOutcome> scaleService(
    String deploymentId,
    String serviceName,
    int replicas, {
    GateOptions gate = GateOptions.none,
  }) => _postAction(
    '/api/deployments/$deploymentId/services/${Uri.encodeComponent(serviceName)}/scale',
    {'replicas': replicas, ...gate.toJson()},
  );

  /// Rolls the whole stack back to exactly one of: an earlier revision
  /// (what actually ran before), a past Compose file version, or a past
  /// Git commit.
  Future<DeploymentActionOutcome> rollbackDeployment(
    String deploymentId, {
    int? revision,
    String? versionId,
    String? gitCommit,
    GateOptions gate = GateOptions.none,
  }) => _postAction('/api/deployments/$deploymentId/rollback', {
    'revision': ?revision,
    'versionId': ?versionId,
    'gitCommit': ?gitCommit,
    ...gate.toJson(),
  });

  /// Promotes a deployment's current revision to another environment/server
  /// as a new deployment, through the target environment's policy.
  Future<DeploymentActionOutcome> promoteDeployment(
    String deploymentId, {
    required String serverId,
    required String environment,
    DeploymentMetadata metadata = const DeploymentMetadata(),
    GateOptions gate = GateOptions.none,
  }) => _postAction('/api/deployments/$deploymentId/promote', {
    ...metadata.toJson(),
    'serverId': serverId,
    'environment': environment,
    ...gate.toJson(),
  });

  Future<List<DeploymentRevision>> listDeploymentRevisions(
    String deploymentId,
  ) async {
    final response = await _http.get(
      Uri.parse('$baseUrl/api/deployments/$deploymentId/revisions'),
      headers: _headers,
    );
    if (response.statusCode != 200) throwApiError(response);
    return (jsonDecode(response.body) as List<dynamic>)
        .map((e) => DeploymentRevision.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  /// One revision including the exact Compose content it ran.
  Future<DeploymentRevision> getDeploymentRevision(
    String deploymentId,
    int revision,
  ) async {
    final response = await _http.get(
      Uri.parse('$baseUrl/api/deployments/$deploymentId/revisions/$revision'),
      headers: _headers,
    );
    if (response.statusCode != 200) throwApiError(response);
    return DeploymentRevision.fromJson(
      jsonDecode(response.body) as Map<String, dynamic>,
    );
  }

  /// "Detect configuration drift" for one deployment.
  Future<DriftReport> getDeploymentDrift(String deploymentId) async {
    final response = await _http.get(
      Uri.parse('$baseUrl/api/deployments/$deploymentId/drift'),
      headers: _headers,
    );
    if (response.statusCode != 200) throwApiError(response);
    return DriftReport.fromJson(
      jsonDecode(response.body) as Map<String, dynamic>,
    );
  }

  /// Which Git-linked deployments have commits on their branch that
  /// aren't deployed yet. Reaches out to every repository, so it can take
  /// a few seconds.
  Future<GitReport> getDeploymentGitReport() async {
    final response = await _http.get(
      Uri.parse('$baseUrl/api/deployments/git-report'),
      headers: _headers,
    );
    if (response.statusCode != 200) throwApiError(response);
    return GitReport.fromJson(
      jsonDecode(response.body) as Map<String, dynamic>,
    );
  }

  /// Approval/scheduled requests, newest first.
  Future<List<DeploymentRequest>> listDeploymentRequests({
    String? status,
    String? deploymentId,
  }) async {
    final response = await _http.get(
      Uri.parse('$baseUrl/api/deployment-requests').replace(
        queryParameters: {
          if (status != null && status.isNotEmpty) 'status': status,
          'deploymentId': ?deploymentId,
        },
      ),
      headers: _headers,
    );
    if (response.statusCode != 200) throwApiError(response);
    return (jsonDecode(response.body) as List<dynamic>)
        .map((e) => DeploymentRequest.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  /// Approves a pending request (admin). It runs now, or at the next
  /// maintenance window unless [overrideMaintenanceWindow].
  Future<DeploymentActionOutcome> approveDeploymentRequest(
    String requestId, {
    String comment = '',
    bool overrideMaintenanceWindow = false,
  }) => _postAction('/api/deployment-requests/$requestId/approve', {
    'comment': comment,
    if (overrideMaintenanceWindow) 'overrideMaintenanceWindow': true,
  });

  Future<void> rejectDeploymentRequest(
    String requestId, {
    String comment = '',
  }) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/deployment-requests/$requestId/reject'),
      headers: _headers,
      body: jsonEncode({'comment': comment}),
    );
    if (response.statusCode != 204) throwApiError(response);
  }

  Future<void> cancelDeploymentRequest(
    String requestId, {
    String comment = '',
  }) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/deployment-requests/$requestId/cancel'),
      headers: _headers,
      body: jsonEncode({'comment': comment}),
    );
    if (response.statusCode != 204) throwApiError(response);
  }

  Future<List<EnvironmentPolicy>> listEnvironmentPolicies() async {
    final response = await _http.get(
      Uri.parse('$baseUrl/api/environment-policies'),
      headers: _headers,
    );
    if (response.statusCode != 200) throwApiError(response);
    return (jsonDecode(response.body) as List<dynamic>)
        .map((e) => EnvironmentPolicy.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  Future<void> updateEnvironmentPolicy(EnvironmentPolicy policy) async {
    final response = await _http.put(
      Uri.parse('$baseUrl/api/environment-policies/${policy.environment}'),
      headers: _headers,
      body: jsonEncode(policy.toJson()),
    );
    if (response.statusCode != 204) throwApiError(response);
  }

  /// "Complete audit trail" (admin), newest first; [before] pages back.
  Future<List<AuditEvent>> listAuditEvents({
    String? entityType,
    String? entityId,
    String? search,
    int? before,
    int limit = 100,
  }) async {
    final response = await _http.get(
      Uri.parse('$baseUrl/api/audit-events').replace(
        queryParameters: {
          if (entityType != null && entityType.isNotEmpty)
            'entityType': entityType,
          'entityId': ?entityId,
          if (search != null && search.isNotEmpty) 'q': search,
          if (before != null) 'before': '$before',
          'limit': '$limit',
        },
      ),
      headers: _headers,
    );
    if (response.statusCode != 200) throwApiError(response);
    return (jsonDecode(response.body) as List<dynamic>)
        .map((e) => AuditEvent.fromJson(e as Map<String, dynamic>))
        .toList();
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

  // ---- Database Marketplace ----

  Future<List<DatabaseEngine>> listDatabaseEngines() async {
    final response = await _http.get(
      Uri.parse('$baseUrl/api/database-engines'),
      headers: _headers,
    );
    if (response.statusCode != 200) throwApiError(response);
    return (jsonDecode(response.body) as List<dynamic>)
        .map((e) => DatabaseEngine.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  /// Validates a wizard submission against the catalog and the target
  /// server and returns the Compose file it would deploy — nothing is
  /// created.
  Future<DatabasePreview> previewDatabase(DatabaseRequest request) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/databases/preview'),
      headers: _headers,
      body: jsonEncode(request.toJson()),
    );
    if (response.statusCode != 200) throwApiError(response);
    return DatabasePreview.fromJson(
      jsonDecode(response.body) as Map<String, dynamic>,
    );
  }

  /// One-click deploy. Credentials are generated and vaulted server-side;
  /// none are returned here — see [revealSecret] / [downloadSecret].
  Future<DatabaseCreateResult> createDatabase(DatabaseRequest request) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/databases'),
      headers: _headers,
      body: jsonEncode(request.toJson()),
    );
    if (response.statusCode != 202) throwApiError(response);
    return DatabaseCreateResult.fromJson(
      jsonDecode(response.body) as Map<String, dynamic>,
    );
  }

  Future<List<DatabaseInstance>> listDatabases() async {
    final response = await _http.get(
      Uri.parse('$baseUrl/api/databases'),
      headers: _headers,
    );
    if (response.statusCode != 200) throwApiError(response);
    return (jsonDecode(response.body) as List<dynamic>)
        .map((e) => DatabaseInstance.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  Future<DatabaseDetail> getDatabase(String id) async {
    final response = await _http.get(
      Uri.parse('$baseUrl/api/databases/$id'),
      headers: _headers,
    );
    if (response.statusCode != 200) throwApiError(response);
    return DatabaseDetail.fromJson(
      jsonDecode(response.body) as Map<String, dynamic>,
    );
  }

  /// [cron] is a 5-field UTC cron expression, or empty for no schedule.
  Future<DatabaseInstance> updateDatabaseBackupPolicy(
    String id, {
    required String cron,
    required bool consistent,
    required int retentionDays,
    required int retentionCount,
  }) async {
    final response = await _http.put(
      Uri.parse('$baseUrl/api/databases/$id/backup-policy'),
      headers: _headers,
      body: jsonEncode({
        'cron': cron,
        'consistent': consistent,
        'retentionDays': retentionDays,
        'retentionCount': retentionCount,
      }),
    );
    if (response.statusCode != 200) throwApiError(response);
    return DatabaseInstance.fromJson(
      jsonDecode(response.body) as Map<String, dynamic>,
    );
  }

  /// Takes a backup now, using the database's consistency setting.
  Future<String> backupDatabase(String id, {bool? consistent}) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/databases/$id/backups'),
      headers: _headers,
      body: consistent == null ? null : jsonEncode({'consistent': consistent}),
    );
    if (response.statusCode != 202) throwApiError(response);
    return (jsonDecode(response.body) as Map<String, dynamic>)['backupId']
        as String;
  }

  Future<Map<String, dynamic>> postgresOverview(String id) async =>
      _postgresObject(id, 'overview');

  Future<DeploymentActionOutcome> reconfigureDatabase(
    String id, {
    required String version,
    required int memoryMb,
    required double cpus,
    required int storageGb,
    GateOptions gate = GateOptions.none,
  }) async {
    final response = await _http.patch(
      Uri.parse('$baseUrl/api/databases/$id/configuration'),
      headers: _headers,
      body: jsonEncode({
        'version': version,
        'memoryMb': memoryMb,
        'cpus': cpus,
        'storageGb': storageGb,
        ...gate.toJson(),
      }),
    );
    if (response.statusCode != 202) throwApiError(response);
    final payload = jsonDecode(response.body) as Map<String, dynamic>;
    return DeploymentActionOutcome.fromJson(
      payload['deployment'] as Map<String, dynamic>,
    );
  }

  Future<void> removeDatabase(String id) async {
    final response = await _http.delete(
      Uri.parse('$baseUrl/api/databases/$id'),
      headers: _headers,
    );
    if (response.statusCode != 204) throwApiError(response);
  }

  Future<void> refreshDatabaseFromBackup(
    String targetId,
    String backupId,
  ) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/databases/$targetId/refresh-from-backup'),
      headers: _headers,
      body: jsonEncode({'backupId': backupId}),
    );
    if (response.statusCode != 202) throwApiError(response);
  }

  Future<String> createPostgresLogicalBackup(String id) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/databases/$id/logical-backups'),
      headers: _headers,
    );
    if (response.statusCode != 202) throwApiError(response);
    return (jsonDecode(response.body) as Map<String, dynamic>)['backupId']
        as String;
  }

  Future<DeploymentActionOutcome> migratePostgresFromBackup(
    String targetId,
    String backupId, {
    GateOptions gate = GateOptions.none,
  }) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/databases/$targetId/migrate-from-backup'),
      headers: _headers,
      body: jsonEncode({'backupId': backupId, ...gate.toJson()}),
    );
    if (response.statusCode != 202) throwApiError(response);
    return DeploymentActionOutcome.fromJson(
      jsonDecode(response.body) as Map<String, dynamic>,
    );
  }

  Future<Map<String, dynamic>> postgresMetrics(String id) async =>
      _postgresObject(id, 'metrics');

  Future<List<Map<String, dynamic>>> postgresAlerts(String id) async {
    final response = await _http.get(
      Uri.parse('$baseUrl/api/databases/$id/postgres/alerts'),
      headers: _headers,
    );
    if (response.statusCode != 200) throwApiError(response);
    return (jsonDecode(response.body) as List<dynamic>)
        .cast<Map<String, dynamic>>();
  }

  Future<Map<String, dynamic>> postgresSizes(String id) async =>
      _postgresObject(id, 'sizes');

  Future<List<Map<String, dynamic>>> postgresSessions(String id) async {
    final response = await _http.get(
      Uri.parse('$baseUrl/api/databases/$id/postgres/sessions'),
      headers: _headers,
    );
    if (response.statusCode != 200) throwApiError(response);
    return (jsonDecode(response.body) as List<dynamic>)
        .cast<Map<String, dynamic>>();
  }

  Future<List<Map<String, dynamic>>> postgresSlowQueries(String id) async {
    final response = await _http.get(
      Uri.parse('$baseUrl/api/databases/$id/postgres/slow-queries'),
      headers: _headers,
    );
    if (response.statusCode != 200) throwApiError(response);
    return (jsonDecode(response.body) as List<dynamic>)
        .cast<Map<String, dynamic>>();
  }

  Future<void> enablePostgresInsights(String id) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/databases/$id/postgres/query-insights/enable'),
      headers: _headers,
    );
    if (response.statusCode != 204) throwApiError(response);
  }

  Future<Map<String, dynamic>> _postgresObject(String id, String path) async {
    final response = await _http.get(
      Uri.parse('$baseUrl/api/databases/$id/postgres/$path'),
      headers: _headers,
    );
    if (response.statusCode != 200) throwApiError(response);
    return jsonDecode(response.body) as Map<String, dynamic>;
  }

  Future<Map<String, dynamic>> testPostgresConnection(String id) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/databases/$id/postgres/connection-test'),
      headers: _headers,
    );
    if (response.statusCode != 200) throwApiError(response);
    return jsonDecode(response.body) as Map<String, dynamic>;
  }

  Future<String> postgresQuery(
    String id,
    String sql, {
    String database = '',
  }) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/databases/$id/postgres/query'),
      headers: _headers,
      body: jsonEncode({'sql': sql, 'database': database}),
    );
    if (response.statusCode != 200) throwApiError(response);
    return response.body;
  }

  Future<void> postgresCreateDatabase(String id, String name) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/databases/$id/postgres/databases'),
      headers: _headers,
      body: jsonEncode({'name': name}),
    );
    if (response.statusCode != 201) throwApiError(response);
  }

  Future<void> postgresDeleteDatabase(String id, String name) async {
    final response = await _http.delete(
      Uri.parse(
        '$baseUrl/api/databases/$id/postgres/databases/${Uri.encodeComponent(name)}',
      ),
      headers: _headers,
    );
    if (response.statusCode != 204) throwApiError(response);
  }

  Future<String> postgresCreateUser(
    String id,
    String username,
    String database,
    String permission,
  ) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/databases/$id/postgres/users'),
      headers: _headers,
      body: jsonEncode({
        'username': username,
        'database': database,
        'permission': permission,
      }),
    );
    if (response.statusCode != 201) throwApiError(response);
    return (jsonDecode(response.body) as Map<String, dynamic>)['secretId']
        as String;
  }

  Future<void> postgresSetPermission(
    String id,
    String username,
    String database,
    String permission,
  ) async {
    final response = await _http.put(
      Uri.parse(
        '$baseUrl/api/databases/$id/postgres/users/${Uri.encodeComponent(username)}/permissions',
      ),
      headers: _headers,
      body: jsonEncode({'database': database, 'permission': permission}),
    );
    if (response.statusCode != 204) throwApiError(response);
  }

  Future<void> postgresTerminateSession(String id, int pid) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/databases/$id/postgres/sessions/$pid/terminate'),
      headers: _headers,
    );
    if (response.statusCode != 204) throwApiError(response);
  }

  Future<void> postgresSetConnectionLimit(
    String id,
    String database,
    int limit,
  ) async {
    final response = await _http.put(
      Uri.parse('$baseUrl/api/databases/$id/postgres/connection-limit'),
      headers: _headers,
      body: jsonEncode({'database': database, 'limit': limit}),
    );
    if (response.statusCode != 204) throwApiError(response);
  }

  Future<List<DatabaseCredential>> listDatabaseCredentials(String id) async {
    final response = await _http.get(
      Uri.parse('$baseUrl/api/databases/$id/credentials'),
      headers: _headers,
    );
    if (response.statusCode != 200) throwApiError(response);
    return (jsonDecode(response.body) as List<dynamic>)
        .map((e) => DatabaseCredential.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  /// Issues a read-only database login that expires after [ttl].
  Future<DatabaseCredential> createTemporaryCredential(
    String databaseId,
    Duration ttl,
  ) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/databases/$databaseId/temporary-credentials'),
      headers: _headers,
      body: jsonEncode({'ttlMinutes': ttl.inMinutes}),
    );
    if (response.statusCode != 201) throwApiError(response);
    return DatabaseCredential.fromJson(
      jsonDecode(response.body) as Map<String, dynamic>,
    );
  }

  /// Decrypts a credential for display. Audited server-side.
  Future<RevealedSecret> revealSecret(String secretId) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/secrets/$secretId/reveal'),
      headers: _headers,
    );
    if (response.statusCode != 200) throwApiError(response);
    return RevealedSecret.fromJson(
      jsonDecode(response.body) as Map<String, dynamic>,
    );
  }

  /// The one-time credentials file (.env format). A second call fails
  /// with 410 until the credential is rotated.
  Future<String> downloadSecret(String secretId) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/secrets/$secretId/download'),
      headers: _headers,
    );
    if (response.statusCode != 200) throwApiError(response);
    return response.body;
  }

  Future<RotationResult> rotateSecret(
    String secretId, {
    GateOptions gate = const GateOptions(),
  }) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/secrets/$secretId/rotate'),
      headers: _headers,
      body: jsonEncode(gate.toJson()),
    );
    if (response.statusCode == 500) {
      final body = jsonDecode(response.body);
      if (body is Map<String, dynamic> && body['newPassword'] is String) {
        throw UnsavedCredentialException(
          500,
          body['error'] as String? ?? 'Rotation could not be saved',
          body['newPassword'] as String,
        );
      }
    }
    if (response.statusCode != 200) throwApiError(response);
    return RotationResult.fromJson(
      jsonDecode(response.body) as Map<String, dynamic>,
    );
  }

  /// Drops a temporary credential's database login now.
  Future<void> revokeSecret(String secretId) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/secrets/$secretId/revoke'),
      headers: _headers,
    );
    if (response.statusCode != 204) throwApiError(response);
  }

  Future<List<SecretGrant>> listSecretGrants(String secretId) async {
    final response = await _http.get(
      Uri.parse('$baseUrl/api/secrets/$secretId/grants'),
      headers: _headers,
    );
    if (response.statusCode != 200) throwApiError(response);
    return (jsonDecode(response.body) as List<dynamic>)
        .map((e) => SecretGrant.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  /// Shares a credential with [userId]; [expiresAt] makes it temporary.
  Future<List<SecretGrant>> shareSecret(
    String secretId,
    String userId, {
    DateTime? expiresAt,
  }) async {
    final response = await _http.put(
      Uri.parse('$baseUrl/api/secrets/$secretId/grants/$userId'),
      headers: _headers,
      body: jsonEncode({
        if (expiresAt != null) 'expiresAt': expiresAt.toUtc().toIso8601String(),
      }),
    );
    if (response.statusCode != 200) throwApiError(response);
    return (jsonDecode(response.body) as List<dynamic>)
        .map((e) => SecretGrant.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  Future<void> unshareSecret(String secretId, String userId) async {
    final response = await _http.delete(
      Uri.parse('$baseUrl/api/secrets/$secretId/grants/$userId'),
      headers: _headers,
    );
    if (response.statusCode != 204) throwApiError(response);
  }

  /// Every user's id and email, for picking whom to share with.
  Future<List<DirectoryUser>> listUserDirectory() async {
    final response = await _http.get(
      Uri.parse('$baseUrl/api/users/directory'),
      headers: _headers,
    );
    if (response.statusCode != 200) throwApiError(response);
    return (jsonDecode(response.body) as List<dynamic>)
        .map((e) => DirectoryUser.fromJson(e as Map<String, dynamic>))
        .toList();
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
    String serverId,
    String containerId,
  ) async {
    final response = await _http.post(
      Uri.parse(
        '$baseUrl/api/servers/$serverId/containers/$containerId/inspect',
      ),
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
      jsonDecode(response.body) as Map<String, dynamic>,
    );
  }

  /// WebSocket URL for one container's live (or bounded, with
  /// `follow: false`) log tail. `withContainers` merges other containers'
  /// logs into the same interleaved feed — "combine logs from related
  /// containers", typically every container in the same deployment. Same
  /// `?token=` query-param auth as [serverStreamUri].
  Uri containerLogsStreamUri(
    String serverId,
    String containerId, {
    bool follow = true,
    int? tail,
    DateTime? since,
    DateTime? until,
    List<String> withContainers = const [],
  }) {
    final httpUri = Uri.parse(baseUrl);
    final wsScheme = httpUri.scheme == 'https' ? 'wss' : 'ws';
    return httpUri.replace(
      scheme: wsScheme,
      path: '/api/servers/$serverId/containers/$containerId/logs/stream',
      queryParameters: {
        'token': ?authToken,
        if (!follow) 'follow': 'false',
        if (tail != null) 'tail': '$tail',
        if (since != null) 'since': since.toUtc().toIso8601String(),
        if (until != null) 'until': until.toUtc().toIso8601String(),
        if (withContainers.isNotEmpty) 'with': withContainers.join(','),
      },
    );
  }

  /// Downloads a bounded log dump as plain text — backs both "download
  /// logs" and, via the OS share sheet on the downloaded file, "share".
  Future<String> downloadContainerLogs(
    String serverId,
    String containerId, {
    int? tail,
    DateTime? since,
    DateTime? until,
  }) async {
    final uri =
        Uri.parse(
          '$baseUrl/api/servers/$serverId/containers/$containerId/logs/download',
        ).replace(
          queryParameters: {
            if (tail != null) 'tail': '$tail',
            if (since != null) 'since': since.toUtc().toIso8601String(),
            if (until != null) 'until': until.toUtc().toIso8601String(),
          },
        );
    final response = await _http.get(uri, headers: _headers);
    if (response.statusCode != 200) {
      throw ApiException(response.statusCode, response.body);
    }
    return response.body;
  }

  /// Bounded history of Docker events for one container (start/stop/die/
  /// health_status/...) — the troubleshooting view's "what happened here".
  Future<List<ContainerEvent>> listContainerEvents(
    String serverId,
    String containerId, {
    DateTime? since,
    DateTime? until,
  }) async {
    final uri =
        Uri.parse(
          '$baseUrl/api/servers/$serverId/containers/$containerId/events',
        ).replace(
          queryParameters: {
            if (since != null) 'since': since.toUtc().toIso8601String(),
            if (until != null) 'until': until.toUtc().toIso8601String(),
          },
        );
    final response = await _http.get(uri, headers: _headers);
    if (response.statusCode != 200) {
      final decoded = jsonDecode(response.body) as Map<String, dynamic>;
      throw ApiException(
        response.statusCode,
        decoded['error'] as String? ?? response.body,
      );
    }
    final decoded = jsonDecode(response.body) as List<dynamic>;
    return decoded
        .map((e) => ContainerEvent.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  /// Whether AI-assisted log summary/root-cause is available — checked up
  /// front so the Flutter app can hide the button instead of only failing
  /// on click.
  Future<bool> aiConfigured() async {
    final response = await _http.get(
      Uri.parse('$baseUrl/api/ai/status'),
      headers: _headers,
    );
    if (response.statusCode != 200) return false;
    final decoded = jsonDecode(response.body) as Map<String, dynamic>;
    return decoded['configured'] as bool? ?? false;
  }

  /// Asks the control plane's AI integration to summarize this container's
  /// recent logs and suggest a root cause.
  Future<LogAnalysis> analyzeContainerLogs(
    String serverId,
    String containerId,
  ) async {
    final response = await _http.post(
      Uri.parse(
        '$baseUrl/api/servers/$serverId/containers/$containerId/logs/analyze',
      ),
      headers: _headers,
    );
    final decoded = jsonDecode(response.body) as Map<String, dynamic>;
    if (response.statusCode != 200) {
      throw ApiException(
        response.statusCode,
        decoded['error'] as String? ?? response.body,
      );
    }
    return LogAnalysis.fromJson(decoded);
  }

  /// WebSocket URL for an interactive `docker exec` terminal session.
  /// Binary frames are pty I/O in both directions; a text frame of the
  /// form "resize:`<cols>x<rows>`" sent by the client resizes the pty. Same
  /// `?token=` query-param auth as [serverStreamUri].
  Uri containerExecUri(
    String serverId,
    String containerId, {
    int cols = 80,
    int rows = 24,
  }) {
    final httpUri = Uri.parse(baseUrl);
    final wsScheme = httpUri.scheme == 'https' ? 'wss' : 'ws';
    return httpUri.replace(
      scheme: wsScheme,
      path: '/api/servers/$serverId/containers/$containerId/exec',
      queryParameters: {'token': ?authToken, 'cols': '$cols', 'rows': '$rows'},
    );
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

  Future<List<ImageBuild>> listDeploymentBuilds(String deploymentId) async {
    final response = await _http.get(
      Uri.parse('$baseUrl/api/deployments/$deploymentId/builds'),
      headers: _headers,
    );
    if (response.statusCode != 200) throwApiError(response);
    return (jsonDecode(response.body) as List<dynamic>)
        .map((item) => ImageBuild.fromJson(item as Map<String, dynamic>))
        .toList();
  }

  Future<ImageBuild> getBuild(String buildId) async {
    final response = await _http.get(
      Uri.parse('$baseUrl/api/builds/$buildId'),
      headers: _headers,
    );
    if (response.statusCode != 200) throwApiError(response);
    return ImageBuild.fromJson(
      jsonDecode(response.body) as Map<String, dynamic>,
    );
  }

  Future<void> cancelBuild(String buildId) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/builds/$buildId/cancel'),
      headers: _headers,
    );
    if (response.statusCode != 204) throwApiError(response);
  }

  Future<String> pushBuild(
    String buildId, {
    required String registryId,
    required String repository,
  }) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/builds/$buildId/push'),
      headers: _headers,
      body: jsonEncode({'registryId': registryId, 'repository': repository}),
    );
    if (response.statusCode != 200) throwApiError(response);
    return (jsonDecode(response.body) as Map<String, dynamic>)['imageRef']
        as String;
  }

  Uri buildStreamUri(String buildId) {
    final httpUri = Uri.parse(baseUrl);
    return httpUri.replace(
      scheme: httpUri.scheme == 'https' ? 'wss' : 'ws',
      path: '/api/builds/$buildId/stream',
      queryParameters: {'token': ?authToken},
    );
  }

  /// Decodes a container lifecycle command's response. A completed round
  /// trip to the agent (whether the operation itself succeeded or failed)
  /// always carries a `success` field; its absence means the command never
  /// reached/answered from the agent at all (409 server not connected, 504
  /// timeout), which is surfaced as an [ApiException] instead.
  ContainerOpResult _decodeOpResult(http.Response response) {
    final decoded = jsonDecode(response.body) as Map<String, dynamic>;
    if (decoded.containsKey('success')) {
      return ContainerOpResult.fromJson(decoded);
    }
    throw ApiException(
      response.statusCode,
      decoded['error'] as String? ?? response.body,
    );
  }

  /// Applies one lifecycle action (start/stop/restart/pause/resume/kill/
  /// remove) to a single container.
  Future<ContainerOpResult> containerAction(
    String serverId,
    String containerId,
    String action, {
    int? timeoutSeconds,
    bool force = false,
  }) async {
    final response = await _http.post(
      Uri.parse(
        '$baseUrl/api/servers/$serverId/containers/$containerId/action',
      ),
      headers: _headers,
      body: jsonEncode({
        'action': action,
        'timeoutSeconds': ?timeoutSeconds,
        if (force) 'force': force,
      }),
    );
    return _decodeOpResult(response);
  }

  /// Applies one action to many containers, possibly spanning multiple
  /// servers. Partial failure isn't an [ApiException] — each target's
  /// outcome is reported individually in the returned list.
  Future<List<BulkActionResult>> bulkContainerAction(
    List<BulkActionTarget> targets,
    String action, {
    int? timeoutSeconds,
    bool force = false,
  }) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/containers/bulk-action'),
      headers: _headers,
      body: jsonEncode({
        'targets': targets.map((t) => t.toJson()).toList(),
        'action': action,
        'timeoutSeconds': ?timeoutSeconds,
        if (force) 'force': force,
      }),
    );
    if (response.statusCode != 200) {
      final decoded = jsonDecode(response.body) as Map<String, dynamic>;
      throw ApiException(
        response.statusCode,
        decoded['error'] as String? ?? response.body,
      );
    }
    final decoded = jsonDecode(response.body) as Map<String, dynamic>;
    return (decoded['results'] as List<dynamic>)
        .map((e) => BulkActionResult.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  /// Creates and starts a new standalone container (not part of a
  /// deployed stack) on [serverId].
  Future<ContainerOpResult> createContainer(
    String serverId,
    ContainerConfig config,
  ) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/servers/$serverId/containers'),
      headers: _headers,
      body: jsonEncode(config.toJson()),
    );
    return _decodeOpResult(response);
  }

  Future<ContainerOpResult> renameContainer(
    String serverId,
    String containerId,
    String newName,
  ) async {
    final response = await _http.post(
      Uri.parse(
        '$baseUrl/api/servers/$serverId/containers/$containerId/rename',
      ),
      headers: _headers,
      body: jsonEncode({'newName': newName}),
    );
    return _decodeOpResult(response);
  }

  /// Duplicates an existing container's configuration into a new,
  /// not-started container.
  Future<ContainerOpResult> cloneContainer(
    String serverId,
    String containerId,
    String newName,
  ) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/servers/$serverId/containers/$containerId/clone'),
      headers: _headers,
      body: jsonEncode({'newName': newName}),
    );
    return _decodeOpResult(response);
  }

  /// Stops and removes an existing container and creates a fresh one in
  /// its place from [config] (typically the old config with edits
  /// applied).
  Future<ContainerOpResult> recreateContainer(
    String serverId,
    String containerId,
    ContainerConfig config,
  ) async {
    final response = await _http.post(
      Uri.parse(
        '$baseUrl/api/servers/$serverId/containers/$containerId/recreate',
      ),
      headers: _headers,
      body: jsonEncode(config.toJson()),
    );
    return _decodeOpResult(response);
  }

  Future<ContainerOpResult> updateRestartPolicy(
    String serverId,
    String containerId,
    String restartPolicyName,
    int restartPolicyMaxRetryCount,
  ) async {
    final response = await _http.patch(
      Uri.parse(
        '$baseUrl/api/servers/$serverId/containers/$containerId/restart-policy',
      ),
      headers: _headers,
      body: jsonEncode({
        'restartPolicyName': restartPolicyName,
        'restartPolicyMaxRetryCount': restartPolicyMaxRetryCount,
      }),
    );
    return _decodeOpResult(response);
  }

  /// Changes an existing container's CPU/memory/process limits live, via
  /// Docker's ContainerUpdate — no recreate needed, same as
  /// [updateRestartPolicy]. 0 on any field means no limit; Docker can only
  /// clear the process limit in place, so clearing a CPU or memory limit
  /// comes back as a failed result telling the user to recreate instead.
  Future<ContainerOpResult> updateResourceLimits(
    String serverId,
    String containerId, {
    int nanoCpus = 0,
    int memoryLimitBytes = 0,
    int memoryReservationBytes = 0,
    int pidsLimit = 0,
  }) async {
    final response = await _http.patch(
      Uri.parse(
        '$baseUrl/api/servers/$serverId/containers/$containerId/resources',
      ),
      headers: _headers,
      body: jsonEncode({
        'nanoCpus': nanoCpus,
        'memoryLimitBytes': memoryLimitBytes,
        'memoryReservationBytes': memoryReservationBytes,
        'pidsLimit': pidsLimit,
      }),
    );
    return _decodeOpResult(response);
  }

  /// [since] is a Go duration string (e.g. '1h', '30m'); defaults to the
  /// server's own default window (1h) if omitted. Per-container counterpart
  /// to [getServerMetrics].
  Future<List<ContainerResourceUsage>> getContainerMetrics(
    String serverId,
    String containerId, {
    String? since,
  }) async {
    final uri = Uri.parse(
      '$baseUrl/api/servers/$serverId/containers/$containerId/metrics',
    ).replace(queryParameters: since == null ? null : {'since': since});
    final response = await _http.get(uri, headers: _headers);
    if (response.statusCode != 200) {
      throw ApiException(response.statusCode, response.body);
    }
    final decoded = jsonDecode(response.body) as List<dynamic>;
    return decoded
        .map((e) => ContainerResourceUsage.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  /// Abnormal-usage findings and right-sizing recommendations for one
  /// container, sized against [limits] (its current limits, from
  /// [inspectContainer] — omit when unknown and every limit is treated as
  /// unset). [since] is a Go duration string; the server defaults to 6h.
  Future<ContainerInsights> getContainerInsights(
    String serverId,
    String containerId, {
    ResourceLimits? limits,
    String? since,
  }) async {
    final uri =
        Uri.parse(
          '$baseUrl/api/servers/$serverId/containers/$containerId/insights',
        ).replace(
          queryParameters: {
            'since': ?since,
            if (limits != null) ...{
              'nanoCpus': '${limits.nanoCpus}',
              'memoryLimitBytes': '${limits.memoryLimitBytes}',
              'memoryReservationBytes': '${limits.memoryReservationBytes}',
              'pidsLimit': '${limits.pidsLimit}',
            },
          },
        );
    final response = await _http.get(uri, headers: _headers);
    if (response.statusCode != 200) throwApiError(response);
    return ContainerInsights.fromJson(
      jsonDecode(response.body) as Map<String, dynamic>,
    );
  }

  Future<List<ContainerAlertRule>> listContainerAlertRules() async {
    final response = await _http.get(
      Uri.parse('$baseUrl/api/container-alert-rules'),
      headers: _headers,
    );
    if (response.statusCode != 200) throwApiError(response);
    final decoded = jsonDecode(response.body) as List<dynamic>;
    return decoded
        .map((e) => ContainerAlertRule.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  /// Creates a rule from [rule] (its id is ignored) and returns the new id.
  Future<String> createContainerAlertRule(ContainerAlertRule rule) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/container-alert-rules'),
      headers: _headers,
      body: jsonEncode(rule.toJson()),
    );
    if (response.statusCode != 201) throwApiError(response);
    return (jsonDecode(response.body) as Map<String, dynamic>)['id'] as String;
  }

  Future<void> updateContainerAlertRule(ContainerAlertRule rule) async {
    final response = await _http.put(
      Uri.parse('$baseUrl/api/container-alert-rules/${rule.id}'),
      headers: _headers,
      body: jsonEncode(rule.toJson()),
    );
    if (response.statusCode != 204) throwApiError(response);
  }

  Future<void> deleteContainerAlertRule(String id) async {
    final response = await _http.delete(
      Uri.parse('$baseUrl/api/container-alert-rules/$id'),
      headers: _headers,
    );
    if (response.statusCode != 204) throwApiError(response);
  }

  /// Open alerts by default; [includeResolved] adds recent history.
  Future<List<ContainerAlert>> listContainerAlerts({
    bool includeResolved = false,
    String? serverId,
    String? containerId,
  }) async {
    final uri = Uri.parse('$baseUrl/api/container-alerts').replace(
      queryParameters: {
        'status': includeResolved ? 'all' : 'open',
        'serverId': ?serverId,
        'containerId': ?containerId,
      },
    );
    final response = await _http.get(uri, headers: _headers);
    if (response.statusCode != 200) throwApiError(response);
    final decoded = jsonDecode(response.body) as List<dynamic>;
    return decoded
        .map((e) => ContainerAlert.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  Future<void> acknowledgeContainerAlert(String id) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/container-alerts/$id/acknowledge'),
      headers: _headers,
    );
    if (response.statusCode != 204) throwApiError(response);
  }

  /// Decodes an image pull/remove/prune command's response, same
  /// "no success field means it never reached the agent" convention as
  /// [_decodeOpResult].
  ImageOpResult _decodeImageOpResult(http.Response response) {
    final decoded = jsonDecode(response.body) as Map<String, dynamic>;
    if (decoded.containsKey('success')) {
      return ImageOpResult.fromJson(decoded);
    }
    throw ApiException(
      response.statusCode,
      decoded['error'] as String? ?? response.body,
    );
  }

  /// Lists the image inventory for one server, or the whole fleet if
  /// [serverId] is omitted (fanned out server-side, live — not a DB read,
  /// since images aren't part of the heartbeat).
  Future<List<ImageSummary>> listImages({String? serverId}) async {
    final uri = Uri.parse('$baseUrl/api/images').replace(
      queryParameters: {
        if (serverId != null && serverId.isNotEmpty) 'serverId': serverId,
      },
    );
    final response = await _http.get(uri, headers: _headers);
    if (response.statusCode != 200) {
      throw ApiException(response.statusCode, response.body);
    }
    final decoded = jsonDecode(response.body) as List<dynamic>;
    return decoded
        .map((e) => ImageSummary.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  /// Fetches the expensive, on-demand fields (layers, env, labels,
  /// architecture) for one image by id.
  Future<ImageDetail> inspectImage(String serverId, String imageId) async {
    final response = await _http.get(
      Uri.parse('$baseUrl/api/servers/$serverId/images/$imageId/inspect'),
      headers: _headers,
    );
    if (response.statusCode != 200) {
      final decoded = jsonDecode(response.body) as Map<String, dynamic>;
      throw ApiException(
        response.statusCode,
        decoded['error'] as String? ?? response.body,
      );
    }
    return ImageDetail.fromJson(
      jsonDecode(response.body) as Map<String, dynamic>,
    );
  }

  /// Pulls [imageRef] onto [serverId], optionally authenticating against a
  /// configured private registry. No progress streaming — this awaits the
  /// full pull, which can take minutes for a large cold image.
  Future<ImageOpResult> pullImage(
    String serverId,
    String imageRef, {
    String? registryId,
  }) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/servers/$serverId/images/pull'),
      headers: _headers,
      body: jsonEncode({
        'imageRef': imageRef,
        if (registryId != null && registryId.isNotEmpty)
          'registryId': registryId,
      }),
    );
    return _decodeImageOpResult(response);
  }

  /// Removes one image on [serverId].
  Future<ImageOpResult> removeImage(
    String serverId,
    String imageId, {
    bool force = false,
  }) async {
    final uri = Uri.parse(
      '$baseUrl/api/servers/$serverId/images/$imageId',
    ).replace(queryParameters: {if (force) 'force': 'true'});
    final response = await _http.delete(uri, headers: _headers);
    return _decodeImageOpResult(response);
  }

  /// Removes dangling (or, with [all], every unused) image on [serverId],
  /// returning the outcome including reclaimed bytes.
  Future<ImageOpResult> pruneImages(String serverId, {bool all = false}) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/servers/$serverId/images/prune'),
      headers: _headers,
      body: jsonEncode({'all': all}),
    );
    return _decodeImageOpResult(response);
  }

  /// Compares a local image's digest against the registry's current digest
  /// for the same tag, to answer "is a newer image available".
  Future<ImageUpdateStatus> imageUpdateAvailable({
    required String serverId,
    required String imageId,
    required String ref,
    String? registryId,
  }) async {
    final uri = Uri.parse('$baseUrl/api/images/newer').replace(
      queryParameters: {
        'serverId': serverId,
        'imageId': imageId,
        'ref': ref,
        if (registryId != null && registryId.isNotEmpty)
          'registryId': registryId,
      },
    );
    final response = await _http.get(uri, headers: _headers);
    if (response.statusCode != 200) {
      final decoded = jsonDecode(response.body) as Map<String, dynamic>;
      throw ApiException(
        response.statusCode,
        decoded['error'] as String? ?? response.body,
      );
    }
    return ImageUpdateStatus.fromJson(
      jsonDecode(response.body) as Map<String, dynamic>,
    );
  }

  /// Lists the network inventory for one server, or the whole fleet if
  /// [serverId] is omitted (fanned out server-side, live — same shape as
  /// [listImages]).
  Future<List<NetworkSummary>> listNetworks({String? serverId}) async {
    final uri = Uri.parse('$baseUrl/api/networks').replace(
      queryParameters: {
        if (serverId != null && serverId.isNotEmpty) 'serverId': serverId,
      },
    );
    final response = await _http.get(uri, headers: _headers);
    if (response.statusCode != 200) {
      throw ApiException(response.statusCode, response.body);
    }
    final decoded = jsonDecode(response.body) as List<dynamic>;
    return decoded
        .map((e) => NetworkSummary.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  /// Creates a new user-defined network on [serverId]. [driver] empty lets
  /// Docker pick its default (bridge).
  Future<NetworkOpResult> createNetwork(
    String serverId, {
    required String name,
    String? driver,
    bool internal = false,
    Map<String, String>? labels,
  }) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/servers/$serverId/networks'),
      headers: _headers,
      body: jsonEncode({
        'name': name,
        if (driver != null && driver.isNotEmpty) 'driver': driver,
        if (internal) 'internal': internal,
        if (labels != null && labels.isNotEmpty) 'labels': labels,
      }),
    );
    return _decodeNetworkOpResult(response);
  }

  /// Removes one network on [serverId].
  Future<NetworkOpResult> removeNetwork(
    String serverId,
    String networkId,
  ) async {
    final response = await _http.delete(
      Uri.parse('$baseUrl/api/servers/$serverId/networks/$networkId'),
      headers: _headers,
    );
    return _decodeNetworkOpResult(response);
  }

  /// Attaches [containerId] to [networkId] on [serverId].
  Future<NetworkOpResult> connectContainerToNetwork(
    String serverId,
    String networkId,
    String containerId,
  ) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/servers/$serverId/networks/$networkId/connect'),
      headers: _headers,
      body: jsonEncode({'containerId': containerId}),
    );
    return _decodeNetworkOpResult(response);
  }

  /// Detaches [containerId] from [networkId] on [serverId].
  Future<NetworkOpResult> disconnectContainerFromNetwork(
    String serverId,
    String networkId,
    String containerId, {
    bool force = false,
  }) async {
    final response = await _http.post(
      Uri.parse(
        '$baseUrl/api/servers/$serverId/networks/$networkId/disconnect',
      ),
      headers: _headers,
      body: jsonEncode({'containerId': containerId, if (force) 'force': true}),
    );
    return _decodeNetworkOpResult(response);
  }

  /// Decodes a network create/remove/connect/disconnect command's response,
  /// same convention as [_decodeImageOpResult].
  NetworkOpResult _decodeNetworkOpResult(http.Response response) {
    final decoded = jsonDecode(response.body) as Map<String, dynamic>;
    if (decoded.containsKey('success')) {
      return NetworkOpResult.fromJson(decoded);
    }
    throw ApiException(
      response.statusCode,
      decoded['error'] as String? ?? response.body,
    );
  }

  /// Lists the volume inventory for one server, or the whole fleet if
  /// [serverId] is omitted — same shape as [listImages]/[listNetworks].
  Future<List<VolumeSummary>> listVolumes({String? serverId}) async {
    final uri = Uri.parse('$baseUrl/api/volumes').replace(
      queryParameters: {
        if (serverId != null && serverId.isNotEmpty) 'serverId': serverId,
      },
    );
    final response = await _http.get(uri, headers: _headers);
    if (response.statusCode != 200) {
      throw ApiException(response.statusCode, response.body);
    }
    final decoded = jsonDecode(response.body) as List<dynamic>;
    return decoded
        .map((e) => VolumeSummary.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  /// Fetches full metadata (driver, mountpoint, labels, size, in-use-by) for
  /// one volume by name, for the metadata-browse detail view.
  Future<VolumeSummary> inspectVolume(String serverId, String name) async {
    final response = await _http.get(
      Uri.parse('$baseUrl/api/servers/$serverId/volumes/$name'),
      headers: _headers,
    );
    if (response.statusCode != 200) {
      final decoded = jsonDecode(response.body) as Map<String, dynamic>;
      throw ApiException(
        response.statusCode,
        decoded['error'] as String? ?? response.body,
      );
    }
    return VolumeSummary.fromJson(
      jsonDecode(response.body) as Map<String, dynamic>,
    );
  }

  Uri _volumeFilesUri(
    String serverId,
    String name,
    String suffix,
    String path,
  ) => Uri.parse(
    '$baseUrl/api/servers/${Uri.encodeComponent(serverId)}/volumes/${Uri.encodeComponent(name)}/files$suffix',
  ).replace(queryParameters: {'path': path});

  Future<List<VolumeFileEntry>> listVolumeFiles(
    String serverId,
    String name, {
    String path = '',
  }) async {
    final response = await _http.get(
      _volumeFilesUri(serverId, name, '', path),
      headers: _headers,
    );
    if (response.statusCode != 200) throwApiError(response);
    return (jsonDecode(response.body) as List<dynamic>)
        .map((e) => VolumeFileEntry.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  Future<List<int>> readVolumeFile(
    String serverId,
    String name,
    String path,
  ) async {
    final response = await _http.get(
      _volumeFilesUri(serverId, name, '/content', path),
      headers: _headers,
    );
    if (response.statusCode != 200) throwApiError(response);
    return response.bodyBytes;
  }

  Future<void> writeVolumeFile(
    String serverId,
    String name,
    String path,
    List<int> content,
  ) async {
    final response = await _http.put(
      _volumeFilesUri(serverId, name, '/content', path),
      headers: {..._headers, 'Content-Type': 'application/octet-stream'},
      body: content,
    );
    if (response.statusCode != 204) throwApiError(response);
  }

  Future<void> cloneVolume(String serverId, String name, String target) async {
    final response = await _http.post(
      Uri.parse(
        '$baseUrl/api/servers/${Uri.encodeComponent(serverId)}/volumes/${Uri.encodeComponent(name)}/clone',
      ),
      headers: _headers,
      body: jsonEncode({'target': target}),
    );
    if (response.statusCode != 201) throwApiError(response);
  }

  /// Creates a new named volume on [serverId].
  Future<VolumeOpResult> createVolume(
    String serverId, {
    required String name,
    String? driver,
    Map<String, String>? labels,
  }) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/servers/$serverId/volumes'),
      headers: _headers,
      body: jsonEncode({
        'name': name,
        if (driver != null && driver.isNotEmpty) 'driver': driver,
        if (labels != null && labels.isNotEmpty) 'labels': labels,
      }),
    );
    return _decodeVolumeOpResult(response);
  }

  /// Removes one volume on [serverId]. Without [force], a volume still in
  /// use by a container comes back as a `success:false` [VolumeOpResult]
  /// (not an [ApiException]) describing which containers are using it — the
  /// caller offers to retry with `force: true`.
  Future<VolumeOpResult> removeVolume(
    String serverId,
    String name, {
    bool force = false,
  }) async {
    final uri = Uri.parse(
      '$baseUrl/api/servers/$serverId/volumes/$name',
    ).replace(queryParameters: {if (force) 'force': 'true'});
    final response = await _http.delete(uri, headers: _headers);
    return _decodeVolumeOpResult(response);
  }

  /// Decodes a volume create/remove command's response, same convention as
  /// [_decodeImageOpResult].
  VolumeOpResult _decodeVolumeOpResult(http.Response response) {
    final decoded = jsonDecode(response.body) as Map<String, dynamic>;
    if (decoded.containsKey('success')) {
      return VolumeOpResult.fromJson(decoded);
    }
    throw ApiException(
      response.statusCode,
      decoded['error'] as String? ?? response.body,
    );
  }

  /// Checks whether [hostPort]/[protocol] is already bound by another
  /// container on [serverId] — used both by the port-mapping form's inline
  /// warning and, redundantly but harmlessly, re-checked server-side on
  /// create/recreate.
  Future<PortConflictResult> checkPortConflict(
    String serverId, {
    required int hostPort,
    String protocol = 'tcp',
  }) async {
    final uri = Uri.parse('$baseUrl/api/servers/$serverId/ports/check').replace(
      queryParameters: {'hostPort': hostPort.toString(), 'protocol': protocol},
    );
    final response = await _http.get(uri, headers: _headers);
    if (response.statusCode != 200) {
      throw ApiException(response.statusCode, response.body);
    }
    return PortConflictResult.fromJson(
      jsonDecode(response.body) as Map<String, dynamic>,
    );
  }

  /// Runs a Trivy vulnerability scan against [imageRef] (pulled from its
  /// registry directly, not from any agent) and stores the result.
  Future<ScanResult> scanImage(String imageRef) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/images/scan'),
      headers: _headers,
      body: jsonEncode({'imageRef': imageRef}),
    );
    if (response.statusCode != 200) {
      final decoded = jsonDecode(response.body) as Map<String, dynamic>;
      throw ApiException(
        response.statusCode,
        decoded['error'] as String? ?? response.body,
      );
    }
    return ScanResult.fromJson(
      jsonDecode(response.body) as Map<String, dynamic>,
    );
  }

  /// Fetches the last stored scan result for [imageRef], or null if none
  /// has been recorded yet.
  Future<ScanResult?> getImageScan(String imageRef) async {
    final response = await _http.get(
      Uri.parse(
        '$baseUrl/api/images/scan',
      ).replace(queryParameters: {'ref': imageRef}),
      headers: _headers,
    );
    if (response.statusCode == 404) return null;
    if (response.statusCode != 200) {
      throw ApiException(response.statusCode, response.body);
    }
    return ScanResult.fromJson(
      jsonDecode(response.body) as Map<String, dynamic>,
    );
  }

  Future<List<Registry>> listRegistries() async {
    final response = await _http.get(
      Uri.parse('$baseUrl/api/registries'),
      headers: _headers,
    );
    if (response.statusCode != 200) {
      throw ApiException(response.statusCode, response.body);
    }
    final decoded = jsonDecode(response.body) as List<dynamic>;
    return decoded
        .map((e) => Registry.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  Future<void> createRegistry({
    required String name,
    required String url,
    String username = '',
    String password = '',
  }) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/registries'),
      headers: _headers,
      body: jsonEncode({
        'name': name,
        'url': url,
        'username': username,
        'password': password,
      }),
    );
    if (response.statusCode != 201) {
      throw ApiException(response.statusCode, response.body);
    }
  }

  Future<void> deleteRegistry(String id) async {
    final response = await _http.delete(
      Uri.parse('$baseUrl/api/registries/$id'),
      headers: _headers,
    );
    if (response.statusCode != 204) {
      throw ApiException(response.statusCode, response.body);
    }
  }

  /// Searches Docker Hub's public catalog (no [registryId]) or a
  /// configured private registry's catalog ([registryId]).
  Future<List<RegistrySearchResult>> searchRegistries({
    String query = '',
    String? registryId,
  }) async {
    final uri = Uri.parse('$baseUrl/api/registries/search').replace(
      queryParameters: {
        'query': query,
        if (registryId != null && registryId.isNotEmpty)
          'registryId': registryId,
      },
    );
    final response = await _http.get(uri, headers: _headers);
    if (response.statusCode != 200) {
      throw ApiException(response.statusCode, response.body);
    }
    final decoded = jsonDecode(response.body) as List<dynamic>;
    return decoded
        .map((e) => RegistrySearchResult.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  /// Lists the published tags for [image], for the "select image version
  /// or tag" picker.
  Future<List<String>> listImageTags(String image, {String? registryId}) async {
    final uri = Uri.parse('$baseUrl/api/registries/tags').replace(
      queryParameters: {
        'image': image,
        if (registryId != null && registryId.isNotEmpty)
          'registryId': registryId,
      },
    );
    final response = await _http.get(uri, headers: _headers);
    if (response.statusCode != 200) {
      throw ApiException(response.statusCode, response.body);
    }
    final decoded = jsonDecode(response.body) as List<dynamic>;
    return decoded.map((e) => e as String).toList();
  }

  Future<List<ApprovedImage>> listApprovedImages() async {
    final response = await _http.get(
      Uri.parse('$baseUrl/api/image-policies'),
      headers: _headers,
    );
    if (response.statusCode != 200) {
      throw ApiException(response.statusCode, response.body);
    }
    final decoded = jsonDecode(response.body) as List<dynamic>;
    return decoded
        .map((e) => ApprovedImage.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  Future<void> createApprovedImage(String pattern, {String note = ''}) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/image-policies'),
      headers: _headers,
      body: jsonEncode({'pattern': pattern, 'note': note}),
    );
    if (response.statusCode != 201) {
      throw ApiException(response.statusCode, response.body);
    }
  }

  Future<void> deleteApprovedImage(String id) async {
    final response = await _http.delete(
      Uri.parse('$baseUrl/api/image-policies/$id'),
      headers: _headers,
    );
    if (response.statusCode != 204) {
      throw ApiException(response.statusCode, response.body);
    }
  }

  Future<bool> getImagePolicyEnabled() async {
    final response = await _http.get(
      Uri.parse('$baseUrl/api/image-policies/settings'),
      headers: _headers,
    );
    if (response.statusCode != 200) {
      throw ApiException(response.statusCode, response.body);
    }
    final decoded = jsonDecode(response.body) as Map<String, dynamic>;
    return decoded['enabled'] as bool? ?? false;
  }

  Future<void> setImagePolicyEnabled(bool enabled) async {
    final response = await _http.patch(
      Uri.parse('$baseUrl/api/image-policies/settings'),
      headers: _headers,
      body: jsonEncode({'enabled': enabled}),
    );
    if (response.statusCode != 204) {
      throw ApiException(response.statusCode, response.body);
    }
  }

  /// Lists a container's recorded previous images, most recent first, so
  /// the UI can show whether "rollback to previous image" is available.
  Future<List<ImageRollbackEntry>> listImageRollbackHistory(
    String serverId,
    String containerId,
  ) async {
    final response = await _http.get(
      Uri.parse(
        '$baseUrl/api/servers/$serverId/containers/$containerId/rollback-history',
      ),
      headers: _headers,
    );
    if (response.statusCode != 200) {
      throw ApiException(response.statusCode, response.body);
    }
    final decoded = jsonDecode(response.body) as List<dynamic>;
    return decoded
        .map((e) => ImageRollbackEntry.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  /// Recreates [containerId] on its most recently recorded previous image,
  /// keeping every other setting unchanged.
  Future<ContainerOpResult> rollbackContainer(
    String serverId,
    String containerId,
  ) async {
    final response = await _http.post(
      Uri.parse(
        '$baseUrl/api/servers/$serverId/containers/$containerId/rollback',
      ),
      headers: _headers,
    );
    return _decodeOpResult(response);
  }

  /// Lists start/stop schedules, optionally narrowed to one server and/or
  /// container.
  Future<List<Schedule>> listSchedules({
    String? serverId,
    String? containerId,
  }) async {
    final uri = Uri.parse('$baseUrl/api/schedules').replace(
      queryParameters: {'serverId': ?serverId, 'containerId': ?containerId},
    );
    final response = await _http.get(uri, headers: _headers);
    if (response.statusCode != 200) {
      throw ApiException(response.statusCode, response.body);
    }
    final decoded = jsonDecode(response.body) as List<dynamic>;
    return decoded
        .map((e) => Schedule.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  /// Creates a recurring ([cronExpr], a standard 5-field UTC cron
  /// expression) or one-time ([runOnceAt], UTC) container start/stop
  /// schedule. Exactly one of cronExpr/runOnceAt must be set, matching
  /// scheduleType.
  Future<String> createSchedule({
    required String serverId,
    required String containerId,
    required String containerName,
    required String action,
    required String scheduleType,
    String? cronExpr,
    DateTime? runOnceAt,
  }) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/schedules'),
      headers: _headers,
      body: jsonEncode({
        'serverId': serverId,
        'containerId': containerId,
        'containerName': containerName,
        'action': action,
        'scheduleType': scheduleType,
        'cronExpr': ?cronExpr,
        if (runOnceAt != null) 'runOnceAt': runOnceAt.toUtc().toIso8601String(),
      }),
    );
    if (response.statusCode != 201) {
      final decoded = jsonDecode(response.body) as Map<String, dynamic>;
      throw ApiException(
        response.statusCode,
        decoded['error'] as String? ?? response.body,
      );
    }
    final decoded = jsonDecode(response.body) as Map<String, dynamic>;
    return decoded['id'] as String;
  }

  Future<void> updateScheduleEnabled(String id, bool enabled) async {
    final response = await _http.patch(
      Uri.parse('$baseUrl/api/schedules/$id'),
      headers: _headers,
      body: jsonEncode({'enabled': enabled}),
    );
    if (response.statusCode != 204) {
      throw ApiException(response.statusCode, response.body);
    }
  }

  Future<void> deleteSchedule(String id) async {
    final response = await _http.delete(
      Uri.parse('$baseUrl/api/schedules/$id'),
      headers: _headers,
    );
    if (response.statusCode != 204) {
      throw ApiException(response.statusCode, response.body);
    }
  }

  // --- Deployment Management > Docker Compose --------------------------

  /// Resolves a stack or compose file's services (image, ports, volumes)
  /// without deploying anything — backs the deploy dialog's "preview
  /// resources before deployment" step.
  Future<DeploymentPreview> previewDeployment({
    String? stackId,
    String? composeFileId,
    String? serverId,
    Map<String, String> env = const {},
  }) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/deployments/preview'),
      headers: _headers,
      body: jsonEncode({
        'stackId': ?stackId,
        'composeFileId': ?composeFileId,
        'serverId': ?serverId,
        'env': env,
      }),
    );
    if (response.statusCode != 200) {
      final decoded = jsonDecode(response.body);
      throw ApiException(
        response.statusCode,
        decoded is Map
            ? (decoded['error'] as String? ?? response.body)
            : response.body,
      );
    }
    return DeploymentPreview.fromJson(
      jsonDecode(response.body) as Map<String, dynamic>,
    );
  }

  Future<List<ComposeFile>> listComposeFiles() async {
    final response = await _http.get(
      Uri.parse('$baseUrl/api/compose-files'),
      headers: _headers,
    );
    if (response.statusCode != 200) {
      throw ApiException(response.statusCode, response.body);
    }
    final decoded = jsonDecode(response.body) as List<dynamic>;
    return decoded
        .map((e) => ComposeFile.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  Future<ComposeFile> getComposeFile(String id) async {
    final response = await _http.get(
      Uri.parse('$baseUrl/api/compose-files/$id'),
      headers: _headers,
    );
    if (response.statusCode != 200) {
      throw ApiException(response.statusCode, response.body);
    }
    return ComposeFile.fromJson(
      jsonDecode(response.body) as Map<String, dynamic>,
    );
  }

  Future<ComposeFile> createComposeFile({
    required String name,
    required String content,
  }) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/compose-files'),
      headers: _headers,
      body: jsonEncode({'name': name, 'content': content}),
    );
    if (response.statusCode != 201) {
      throw ApiException(response.statusCode, response.body);
    }
    return ComposeFile.fromJson(
      jsonDecode(response.body) as Map<String, dynamic>,
    );
  }

  Future<ComposeFile> updateComposeFile(
    String id, {
    required String name,
    required String content,
  }) async {
    final response = await _http.patch(
      Uri.parse('$baseUrl/api/compose-files/$id'),
      headers: _headers,
      body: jsonEncode({'name': name, 'content': content}),
    );
    if (response.statusCode != 200) {
      throw ApiException(response.statusCode, response.body);
    }
    return ComposeFile.fromJson(
      jsonDecode(response.body) as Map<String, dynamic>,
    );
  }

  Future<void> deleteComposeFile(String id) async {
    final response = await _http.delete(
      Uri.parse('$baseUrl/api/compose-files/$id'),
      headers: _headers,
    );
    if (response.statusCode != 204) throwApiError(response);
  }

  Future<List<ComposeFileVersionSummary>> listComposeFileVersions(
    String composeFileId,
  ) async {
    final response = await _http.get(
      Uri.parse('$baseUrl/api/compose-files/$composeFileId/versions'),
      headers: _headers,
    );
    if (response.statusCode != 200) {
      throw ApiException(response.statusCode, response.body);
    }
    final decoded = jsonDecode(response.body) as List<dynamic>;
    return decoded
        .map(
          (e) => ComposeFileVersionSummary.fromJson(e as Map<String, dynamic>),
        )
        .toList();
  }

  Future<ComposeFileVersion> getComposeFileVersion(
    String composeFileId,
    String versionId,
  ) async {
    final response = await _http.get(
      Uri.parse(
        '$baseUrl/api/compose-files/$composeFileId/versions/$versionId',
      ),
      headers: _headers,
    );
    if (response.statusCode != 200) {
      throw ApiException(response.statusCode, response.body);
    }
    return ComposeFileVersion.fromJson(
      jsonDecode(response.body) as Map<String, dynamic>,
    );
  }

  /// Makes a past version the current content again — itself undoable,
  /// since UpdateComposeFile snapshots whatever was current before
  /// overwriting it.
  Future<ComposeFile> restoreComposeFileVersion(
    String composeFileId,
    String versionId,
  ) async {
    final response = await _http.post(
      Uri.parse(
        '$baseUrl/api/compose-files/$composeFileId/versions/$versionId/restore',
      ),
      headers: _headers,
    );
    if (response.statusCode != 200) {
      throw ApiException(response.statusCode, response.body);
    }
    return ComposeFile.fromJson(
      jsonDecode(response.body) as Map<String, dynamic>,
    );
  }

  /// Validates raw Compose YAML and, when possible, projects it into
  /// [ComposeServiceDraft]s for the visual editor — shared by the YAML
  /// editor's live validation and "switch to visual mode".
  Future<ComposeParseResult> parseComposeYaml(String content) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/compose-files/parse'),
      headers: _headers,
      body: jsonEncode({'content': content}),
    );
    if (response.statusCode != 200) {
      throw ApiException(response.statusCode, response.body);
    }
    return ComposeParseResult.fromJson(
      jsonDecode(response.body) as Map<String, dynamic>,
    );
  }

  /// Renders the visual editor's in-progress services into Compose YAML —
  /// backs "switch to YAML mode".
  Future<String> renderComposeYaml(List<ComposeServiceDraft> services) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/compose-files/render'),
      headers: _headers,
      body: jsonEncode({'services': services.map((s) => s.toJson()).toList()}),
    );
    if (response.statusCode != 200) {
      throw ApiException(response.statusCode, response.body);
    }
    final decoded = jsonDecode(response.body) as Map<String, dynamic>;
    return decoded['content'] as String;
  }

  // --- Deployment Management > Docker Compose > Configuration ----------

  Future<List<EnvVarGroup>> listEnvVarGroups({String? environment}) async {
    final uri = Uri.parse('$baseUrl/api/env-var-groups').replace(
      queryParameters: {
        if (environment != null && environment.isNotEmpty)
          'environment': environment,
      },
    );
    final response = await _http.get(uri, headers: _headers);
    if (response.statusCode != 200) {
      throw ApiException(response.statusCode, response.body);
    }
    final decoded = jsonDecode(response.body) as List<dynamic>;
    return decoded
        .map((e) => EnvVarGroup.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  Future<EnvVarGroup> getEnvVarGroup(String id) async {
    final response = await _http.get(
      Uri.parse('$baseUrl/api/env-var-groups/$id'),
      headers: _headers,
    );
    if (response.statusCode != 200) {
      throw ApiException(response.statusCode, response.body);
    }
    return EnvVarGroup.fromJson(
      jsonDecode(response.body) as Map<String, dynamic>,
    );
  }

  Future<EnvVarGroup> createEnvVarGroup({
    required String name,
    required String environment,
    required List<EnvVariable> variables,
  }) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/env-var-groups'),
      headers: _headers,
      body: jsonEncode({
        'name': name,
        'environment': environment,
        'variables': variables.map((v) => v.toJson()).toList(),
      }),
    );
    if (response.statusCode != 201) {
      throw ApiException(response.statusCode, response.body);
    }
    return EnvVarGroup.fromJson(
      jsonDecode(response.body) as Map<String, dynamic>,
    );
  }

  Future<EnvVarGroup> updateEnvVarGroup(
    String id, {
    required String name,
    required String environment,
    required List<EnvVariable> variables,
  }) async {
    final response = await _http.patch(
      Uri.parse('$baseUrl/api/env-var-groups/$id'),
      headers: _headers,
      body: jsonEncode({
        'name': name,
        'environment': environment,
        'variables': variables.map((v) => v.toJson()).toList(),
      }),
    );
    if (response.statusCode != 200) {
      throw ApiException(response.statusCode, response.body);
    }
    return EnvVarGroup.fromJson(
      jsonDecode(response.body) as Map<String, dynamic>,
    );
  }

  Future<void> deleteEnvVarGroup(String id) async {
    final response = await _http.delete(
      Uri.parse('$baseUrl/api/env-var-groups/$id'),
      headers: _headers,
    );
    if (response.statusCode != 204) {
      throw ApiException(response.statusCode, response.body);
    }
  }

  // --- Git-based deployment -------------------------------------------

  Future<List<GitRepository>> listGitRepositories() async {
    final response = await _http.get(
      Uri.parse('$baseUrl/api/git-repositories'),
      headers: _headers,
    );
    if (response.statusCode != 200) throwApiError(response);
    return (jsonDecode(response.body) as List<dynamic>)
        .map((e) => GitRepository.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  /// Adds a repository (admin). The control plane checks it's reachable
  /// first; the returned webhook info is how to configure push webhooks.
  Future<(GitRepository, GitWebhookInfo)> createGitRepository({
    required String name,
    required String provider,
    required String url,
    String username = '',
    String token = '',
    String defaultBranch = 'main',
  }) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/git-repositories'),
      headers: _headers,
      body: jsonEncode({
        'name': name,
        'provider': provider,
        'url': url,
        'username': username,
        if (token.isNotEmpty) 'token': token,
        'defaultBranch': defaultBranch,
      }),
    );
    if (response.statusCode != 201) throwApiError(response);
    final decoded = jsonDecode(response.body) as Map<String, dynamic>;
    return (
      GitRepository.fromJson(decoded['repository'] as Map<String, dynamic>),
      GitWebhookInfo.fromJson(decoded['webhook'] as Map<String, dynamic>),
    );
  }

  /// Edits a repository (admin). [token] null keeps the stored one.
  Future<GitRepository> updateGitRepository(
    String id, {
    required String name,
    required String provider,
    required String url,
    String username = '',
    String? token,
    String defaultBranch = 'main',
  }) async {
    final response = await _http.patch(
      Uri.parse('$baseUrl/api/git-repositories/$id'),
      headers: _headers,
      body: jsonEncode({
        'name': name,
        'provider': provider,
        'url': url,
        'username': username,
        'token': ?token,
        'defaultBranch': defaultBranch,
      }),
    );
    if (response.statusCode != 200) throwApiError(response);
    return GitRepository.fromJson(
      jsonDecode(response.body) as Map<String, dynamic>,
    );
  }

  Future<void> deleteGitRepository(String id) async {
    final response = await _http.delete(
      Uri.parse('$baseUrl/api/git-repositories/$id'),
      headers: _headers,
    );
    if (response.statusCode != 204) throwApiError(response);
  }

  Future<GitWebhookInfo> getGitWebhook(String id) async {
    final response = await _http.get(
      Uri.parse('$baseUrl/api/git-repositories/$id/webhook'),
      headers: _headers,
    );
    if (response.statusCode != 200) throwApiError(response);
    return GitWebhookInfo.fromJson(
      jsonDecode(response.body) as Map<String, dynamic>,
    );
  }

  Future<GitWebhookInfo> rotateGitWebhookSecret(String id) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/git-repositories/$id/webhook/rotate'),
      headers: _headers,
    );
    if (response.statusCode != 200) throwApiError(response);
    return GitWebhookInfo.fromJson(
      jsonDecode(response.body) as Map<String, dynamic>,
    );
  }

  Future<GitRefs> listGitRefs(String repositoryId) async {
    final response = await _http.get(
      Uri.parse('$baseUrl/api/git-repositories/$repositoryId/refs'),
      headers: _headers,
    );
    if (response.statusCode != 200) throwApiError(response);
    return GitRefs.fromJson(jsonDecode(response.body) as Map<String, dynamic>);
  }

  /// Fetches [path] at [ref] with its Compose validation, for the import
  /// dialog's preview.
  Future<({String content, String commit, ComposeParseResult parse})>
  previewGitFile(
    String repositoryId, {
    required String ref,
    required String path,
  }) async {
    final response = await _http.get(
      Uri.parse(
        '$baseUrl/api/git-repositories/$repositoryId/file',
      ).replace(queryParameters: {'ref': ref, 'path': path}),
      headers: _headers,
    );
    if (response.statusCode != 200) throwApiError(response);
    final decoded = jsonDecode(response.body) as Map<String, dynamic>;
    return (
      content: decoded['content'] as String,
      commit: decoded['commit'] as String,
      parse: ComposeParseResult.fromJson(
        decoded['parse'] as Map<String, dynamic>,
      ),
    );
  }

  /// "Import Compose files from Git".
  Future<ComposeFile> importGitComposeFile(
    String repositoryId, {
    required String name,
    required String ref,
    required String path,
  }) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/git-repositories/$repositoryId/import'),
      headers: _headers,
      body: jsonEncode({'name': name, 'ref': ref, 'path': path}),
    );
    if (response.statusCode != 201) throwApiError(response);
    return ComposeFile.fromJson(
      jsonDecode(response.body) as Map<String, dynamic>,
    );
  }

  Future<ComposeFile> generateGitComposeFile(
    String repositoryId, {
    required String name,
    required String ref,
    required String contextPath,
    required String dockerfile,
    required int port,
    List<String> envKeys = const [],
  }) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/git-repositories/$repositoryId/generate-compose'),
      headers: _headers,
      body: jsonEncode({
        'name': name,
        'ref': ref,
        'contextPath': contextPath,
        'dockerfile': dockerfile,
        'port': port,
        'envKeys': envKeys,
      }),
    );
    if (response.statusCode != 201) throwApiError(response);
    return ComposeFile.fromJson(
      jsonDecode(response.body) as Map<String, dynamic>,
    );
  }

  /// Pulls a Git-linked Compose file's latest content from its branch/tag.
  Future<({bool changed, String commit, ComposeFile file})> syncComposeFile(
    String composeFileId,
  ) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/compose-files/$composeFileId/git/sync'),
      headers: _headers,
    );
    if (response.statusCode != 200) throwApiError(response);
    final decoded = jsonDecode(response.body) as Map<String, dynamic>;
    return (
      changed: decoded['changed'] as bool? ?? false,
      commit: decoded['commit'] as String? ?? '',
      file: ComposeFile.fromJson(decoded['file'] as Map<String, dynamic>),
    );
  }

  /// Links a Compose file to a repository path at a branch/tag (and syncs
  /// it), or unlinks it when [repositoryId] is null.
  Future<ComposeFile> setComposeFileGitLink(
    String composeFileId, {
    String? repositoryId,
    String ref = '',
    String path = '',
  }) async {
    final response = await _http.put(
      Uri.parse('$baseUrl/api/compose-files/$composeFileId/git'),
      headers: _headers,
      body: jsonEncode({
        'repositoryId': repositoryId,
        'ref': ref,
        'path': path,
      }),
    );
    if (response.statusCode != 200) throwApiError(response);
    return ComposeFile.fromJson(
      jsonDecode(response.body) as Map<String, dynamic>,
    );
  }

  /// Recent commits that changed a Git-linked Compose file — the choices
  /// for "Roll back to a previous commit".
  Future<List<GitCommit>> listComposeFileCommits(
    String composeFileId, {
    String? ref,
    int limit = 30,
  }) async {
    final response = await _http.get(
      Uri.parse(
        '$baseUrl/api/compose-files/$composeFileId/git/commits',
      ).replace(
        queryParameters: {
          if (ref != null && ref.isNotEmpty) 'ref': ref,
          'limit': '$limit',
        },
      ),
      headers: _headers,
    );
    if (response.statusCode != 200) throwApiError(response);
    return (jsonDecode(response.body) as List<dynamic>)
        .map((e) => GitCommit.fromJson(e as Map<String, dynamic>))
        .toList();
  }
}
