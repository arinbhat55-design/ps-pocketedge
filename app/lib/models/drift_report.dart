/// "Detect configuration drift" — GET /api/deployments/{id}/drift: how the
/// deployment's actual state differs from what it should be.
class DriftReport {
  final DateTime checkedAt;
  final bool drifted;
  final ConfigDrift config;
  final GitDrift? git;
  final RuntimeDrift runtime;

  const DriftReport({
    required this.checkedAt,
    required this.drifted,
    required this.config,
    this.git,
    required this.runtime,
  });

  factory DriftReport.fromJson(Map<String, dynamic> json) {
    return DriftReport(
      checkedAt: DateTime.parse(json['checkedAt'] as String),
      drifted: json['drifted'] as bool? ?? false,
      config: ConfigDrift.fromJson(
        json['config'] as Map<String, dynamic>? ?? const {},
      ),
      git: json['git'] == null
          ? null
          : GitDrift.fromJson(json['git'] as Map<String, dynamic>),
      runtime: RuntimeDrift.fromJson(
        json['runtime'] as Map<String, dynamic>? ?? const {},
      ),
    );
  }
}

/// The deployment's source changed since the current revision was
/// deployed — a redeploy would change things.
class ConfigDrift {
  final bool drifted;
  final int deployedRevision;
  final int? deployedVersion;
  final int? currentVersion;
  final String reason;
  final String deployedContent;
  final String currentContent;

  const ConfigDrift({
    this.drifted = false,
    this.deployedRevision = 0,
    this.deployedVersion,
    this.currentVersion,
    this.reason = '',
    this.deployedContent = '',
    this.currentContent = '',
  });

  factory ConfigDrift.fromJson(Map<String, dynamic> json) {
    return ConfigDrift(
      drifted: json['drifted'] as bool? ?? false,
      deployedRevision: (json['deployedRevision'] as num?)?.toInt() ?? 0,
      deployedVersion: (json['deployedVersion'] as num?)?.toInt(),
      currentVersion: (json['currentVersion'] as num?)?.toInt(),
      reason: json['reason'] as String? ?? '',
      deployedContent: json['deployedContent'] as String? ?? '',
      currentContent: json['currentContent'] as String? ?? '',
    );
  }
}

/// The tracked branch moved past the deployed commit.
class GitDrift {
  final bool drifted;
  final String ref;
  final String deployedCommit;
  final String latestCommit;
  final String error;

  const GitDrift({
    this.drifted = false,
    this.ref = '',
    this.deployedCommit = '',
    this.latestCommit = '',
    this.error = '',
  });

  factory GitDrift.fromJson(Map<String, dynamic> json) {
    return GitDrift(
      drifted: json['drifted'] as bool? ?? false,
      ref: json['ref'] as String? ?? '',
      deployedCommit: json['deployedCommit'] as String? ?? '',
      latestCommit: json['latestCommit'] as String? ?? '',
      error: json['error'] as String? ?? '',
    );
  }
}

/// The containers on the server (as last reported by its agent) don't
/// match what the current revision defines.
class RuntimeDrift {
  final bool drifted;
  final List<String> missing;
  final List<String> unexpected;
  final List<String> notRunning;
  final List<String> imageMismatch;
  final DateTime? inventoryAt;

  const RuntimeDrift({
    this.drifted = false,
    this.missing = const [],
    this.unexpected = const [],
    this.notRunning = const [],
    this.imageMismatch = const [],
    this.inventoryAt,
  });

  factory RuntimeDrift.fromJson(Map<String, dynamic> json) {
    List<String> list(Object? v) =>
        (v as List<dynamic>? ?? []).map((e) => e as String).toList();
    return RuntimeDrift(
      drifted: json['drifted'] as bool? ?? false,
      missing: list(json['missing']),
      unexpected: list(json['unexpected']),
      notRunning: list(json['notRunning']),
      imageMismatch: list(json['imageMismatch']),
      inventoryAt: json['inventoryAt'] == null
          ? null
          : DateTime.parse(json['inventoryAt'] as String),
    );
  }
}
