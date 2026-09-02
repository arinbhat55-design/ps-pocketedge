import 'dart:convert';

import 'package:http/http.dart' as http;

import '../models/backup.dart';
import '../models/container.dart';
import '../models/image.dart';
import '../models/schedule.dart';
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
    final response = await _http.get(
      Uri.parse('$baseUrl/api/auth/me'),
      headers: _headers,
    );
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
        if (timeoutSeconds != null) 'timeoutSeconds': timeoutSeconds,
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
        if (timeoutSeconds != null) 'timeoutSeconds': timeoutSeconds,
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
    return ImageDetail.fromJson(jsonDecode(response.body) as Map<String, dynamic>);
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
    return ScanResult.fromJson(jsonDecode(response.body) as Map<String, dynamic>);
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
    return ScanResult.fromJson(jsonDecode(response.body) as Map<String, dynamic>);
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
      queryParameters: {
        if (serverId != null) 'serverId': serverId,
        if (containerId != null) 'containerId': containerId,
      },
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
        if (cronExpr != null) 'cronExpr': cronExpr,
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
}
