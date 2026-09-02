/// The visual editor's simplified view of one Compose service — a lossy
/// projection of the full Compose spec (image, command, ports, environment,
/// volumes, restart only). Mirrors compose.ServiceDraft on the control
/// plane, which is the source of truth for both directions of the
/// visual/YAML conversion (this app never parses or generates YAML itself).
class ComposeServiceDraft {
  final String name;
  final String image;
  final String command;
  final List<String> ports;
  final Map<String, String> environment;
  final List<String> volumes;
  final String restart;
  // deploy.resources.limits/reservations — same nanoCPUs/bytes units the
  // standalone container resource-limit fields already use. 0 = unset.
  final int nanoCpus;
  final int memoryLimitBytes;
  final int memoryReservationBytes;
  // healthCheckTest empty means no healthcheck declared; the other
  // healthCheck* fields are meaningless when it's empty. Interval/
  // timeout/startPeriod are in seconds.
  final String healthCheckTest;
  final int healthCheckIntervalSeconds;
  final int healthCheckTimeoutSeconds;
  final int healthCheckRetries;
  final int healthCheckStartPeriodSeconds;

  const ComposeServiceDraft({
    required this.name,
    this.image = '',
    this.command = '',
    this.ports = const [],
    this.environment = const {},
    this.volumes = const [],
    this.restart = '',
    this.nanoCpus = 0,
    this.memoryLimitBytes = 0,
    this.memoryReservationBytes = 0,
    this.healthCheckTest = '',
    this.healthCheckIntervalSeconds = 0,
    this.healthCheckTimeoutSeconds = 0,
    this.healthCheckRetries = 0,
    this.healthCheckStartPeriodSeconds = 0,
  });

  factory ComposeServiceDraft.fromJson(Map<String, dynamic> json) {
    return ComposeServiceDraft(
      name: json['name'] as String? ?? '',
      image: json['image'] as String? ?? '',
      command: json['command'] as String? ?? '',
      ports: (json['ports'] as List<dynamic>? ?? [])
          .map((e) => e as String)
          .toList(),
      environment: (json['environment'] as Map<String, dynamic>? ?? {}).map(
        (k, v) => MapEntry(k, v as String),
      ),
      volumes: (json['volumes'] as List<dynamic>? ?? [])
          .map((e) => e as String)
          .toList(),
      restart: json['restart'] as String? ?? '',
      nanoCpus: (json['nanoCpus'] as num?)?.toInt() ?? 0,
      memoryLimitBytes: (json['memoryLimitBytes'] as num?)?.toInt() ?? 0,
      memoryReservationBytes:
          (json['memoryReservationBytes'] as num?)?.toInt() ?? 0,
      healthCheckTest: json['healthCheckTest'] as String? ?? '',
      healthCheckIntervalSeconds:
          (json['healthCheckIntervalSeconds'] as num?)?.toInt() ?? 0,
      healthCheckTimeoutSeconds:
          (json['healthCheckTimeoutSeconds'] as num?)?.toInt() ?? 0,
      healthCheckRetries: (json['healthCheckRetries'] as num?)?.toInt() ?? 0,
      healthCheckStartPeriodSeconds:
          (json['healthCheckStartPeriodSeconds'] as num?)?.toInt() ?? 0,
    );
  }

  Map<String, dynamic> toJson() {
    return {
      'name': name,
      'image': image,
      if (command.isNotEmpty) 'command': command,
      if (ports.isNotEmpty) 'ports': ports,
      if (environment.isNotEmpty) 'environment': environment,
      if (volumes.isNotEmpty) 'volumes': volumes,
      if (restart.isNotEmpty) 'restart': restart,
      if (nanoCpus > 0) 'nanoCpus': nanoCpus,
      if (memoryLimitBytes > 0) 'memoryLimitBytes': memoryLimitBytes,
      if (memoryReservationBytes > 0)
        'memoryReservationBytes': memoryReservationBytes,
      if (healthCheckTest.isNotEmpty) ...{
        'healthCheckTest': healthCheckTest,
        if (healthCheckIntervalSeconds > 0)
          'healthCheckIntervalSeconds': healthCheckIntervalSeconds,
        if (healthCheckTimeoutSeconds > 0)
          'healthCheckTimeoutSeconds': healthCheckTimeoutSeconds,
        if (healthCheckRetries > 0) 'healthCheckRetries': healthCheckRetries,
        if (healthCheckStartPeriodSeconds > 0)
          'healthCheckStartPeriodSeconds': healthCheckStartPeriodSeconds,
      },
    };
  }

  ComposeServiceDraft copyWith({
    String? name,
    String? image,
    String? command,
    List<String>? ports,
    Map<String, String>? environment,
    List<String>? volumes,
    String? restart,
    int? nanoCpus,
    int? memoryLimitBytes,
    int? memoryReservationBytes,
    String? healthCheckTest,
    int? healthCheckIntervalSeconds,
    int? healthCheckTimeoutSeconds,
    int? healthCheckRetries,
    int? healthCheckStartPeriodSeconds,
  }) {
    return ComposeServiceDraft(
      name: name ?? this.name,
      image: image ?? this.image,
      command: command ?? this.command,
      ports: ports ?? this.ports,
      environment: environment ?? this.environment,
      volumes: volumes ?? this.volumes,
      restart: restart ?? this.restart,
      nanoCpus: nanoCpus ?? this.nanoCpus,
      memoryLimitBytes: memoryLimitBytes ?? this.memoryLimitBytes,
      memoryReservationBytes:
          memoryReservationBytes ?? this.memoryReservationBytes,
      healthCheckTest: healthCheckTest ?? this.healthCheckTest,
      healthCheckIntervalSeconds:
          healthCheckIntervalSeconds ?? this.healthCheckIntervalSeconds,
      healthCheckTimeoutSeconds:
          healthCheckTimeoutSeconds ?? this.healthCheckTimeoutSeconds,
      healthCheckRetries: healthCheckRetries ?? this.healthCheckRetries,
      healthCheckStartPeriodSeconds:
          healthCheckStartPeriodSeconds ?? this.healthCheckStartPeriodSeconds,
    );
  }
}

/// Result of POST /api/compose-files/parse: whether [content] is valid
/// Compose YAML, and — when [visualEditable] — the services projected out
/// of it for the visual editor to load.
class ComposeParseResult {
  final bool valid;
  final List<String> errors;
  final List<String> serviceNames;
  final bool visualEditable;
  final List<ComposeServiceDraft> services;

  const ComposeParseResult({
    required this.valid,
    this.errors = const [],
    this.serviceNames = const [],
    this.visualEditable = false,
    this.services = const [],
  });

  factory ComposeParseResult.fromJson(Map<String, dynamic> json) {
    return ComposeParseResult(
      valid: json['valid'] as bool? ?? false,
      errors: (json['errors'] as List<dynamic>? ?? [])
          .map((e) => e as String)
          .toList(),
      serviceNames: (json['serviceNames'] as List<dynamic>? ?? [])
          .map((e) => e as String)
          .toList(),
      visualEditable: json['visualEditable'] as bool? ?? false,
      services: (json['services'] as List<dynamic>? ?? [])
          .map((e) => ComposeServiceDraft.fromJson(e as Map<String, dynamic>))
          .toList(),
    );
  }
}

/// A user-authored Docker Compose file, managed under Deployment
/// Management > Docker Compose. Distinct from [StackSummary]'s curated
/// one-click-deploy catalog.
class ComposeFile {
  final String id;
  final String name;
  final String content;
  final int version;
  final List<String> serviceNames;
  final DateTime createdAt;
  final DateTime updatedAt;

  const ComposeFile({
    required this.id,
    required this.name,
    required this.content,
    this.version = 1,
    this.serviceNames = const [],
    required this.createdAt,
    required this.updatedAt,
  });

  factory ComposeFile.fromJson(Map<String, dynamic> json) {
    return ComposeFile(
      id: json['id'] as String,
      name: json['name'] as String? ?? '',
      content: json['content'] as String? ?? '',
      version: (json['version'] as num?)?.toInt() ?? 1,
      serviceNames: (json['serviceNames'] as List<dynamic>? ?? [])
          .map((e) => e as String)
          .toList(),
      createdAt: DateTime.parse(json['createdAt'] as String),
      updatedAt: DateTime.parse(json['updatedAt'] as String),
    );
  }
}

/// One entry in a compose file's version history, without its content —
/// GET /api/compose-files/{id}/versions. See [ComposeFileVersion] for a
/// single version's full content.
class ComposeFileVersionSummary {
  final String id;
  final int versionNumber;
  final String name;
  final DateTime createdAt;

  const ComposeFileVersionSummary({
    required this.id,
    required this.versionNumber,
    required this.name,
    required this.createdAt,
  });

  factory ComposeFileVersionSummary.fromJson(Map<String, dynamic> json) {
    return ComposeFileVersionSummary(
      id: json['id'] as String,
      versionNumber: (json['versionNumber'] as num?)?.toInt() ?? 0,
      name: json['name'] as String? ?? '',
      createdAt: DateTime.parse(json['createdAt'] as String),
    );
  }
}

/// One full snapshot from GET /api/compose-files/{id}/versions/{versionId}
/// — a past version's complete content, for viewing, comparing, or
/// restoring.
class ComposeFileVersion {
  final String id;
  final String composeFileId;
  final int versionNumber;
  final String name;
  final String content;
  final DateTime createdAt;

  const ComposeFileVersion({
    required this.id,
    required this.composeFileId,
    required this.versionNumber,
    required this.name,
    required this.content,
    required this.createdAt,
  });

  factory ComposeFileVersion.fromJson(Map<String, dynamic> json) {
    return ComposeFileVersion(
      id: json['id'] as String,
      composeFileId: json['composeFileId'] as String? ?? '',
      versionNumber: (json['versionNumber'] as num?)?.toInt() ?? 0,
      name: json['name'] as String? ?? '',
      content: json['content'] as String? ?? '',
      createdAt: DateTime.parse(json['createdAt'] as String),
    );
  }
}
