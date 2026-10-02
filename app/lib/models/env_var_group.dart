/// One variable in an [EnvVarGroup] — same shape as the control plane's
/// store.Parameter (key/label/default/secret), reused here under its own
/// name since "a saved env var" and "a catalog stack's fillable parameter"
/// are different concepts even though they serialize the same way.
class EnvVariable {
  final String key;
  final String label;
  final String value;
  final bool secret;

  const EnvVariable({
    required this.key,
    this.label = '',
    this.value = '',
    this.secret = false,
  });

  factory EnvVariable.fromJson(Map<String, dynamic> json) {
    return EnvVariable(
      key: json['key'] as String? ?? '',
      label: json['label'] as String? ?? '',
      value: json['default'] as String? ?? '',
      secret: json['secret'] as bool? ?? false,
    );
  }

  Map<String, dynamic> toJson() {
    return {'key': key, 'label': label, 'default': value, 'secret': secret};
  }

  EnvVariable copyWith({
    String? key,
    String? label,
    String? value,
    bool? secret,
  }) {
    return EnvVariable(
      key: key ?? this.key,
      label: label ?? this.label,
      value: value ?? this.value,
      secret: secret ?? this.secret,
    );
  }
}

/// Development/test/staging/production — "Development, test, staging, and
/// production profiles".
const kEnvironments = ['development', 'test', 'staging', 'production'];

/// A reusable, named set of environment variables tagged to a deployment
/// profile — GET/POST/PATCH/DELETE /api/env-var-groups.
class EnvVarGroup {
  final String id;
  final String name;
  final String environment;
  final List<EnvVariable> variables;
  final DateTime createdAt;
  final DateTime updatedAt;

  const EnvVarGroup({
    required this.id,
    required this.name,
    required this.environment,
    this.variables = const [],
    required this.createdAt,
    required this.updatedAt,
  });

  factory EnvVarGroup.fromJson(Map<String, dynamic> json) {
    return EnvVarGroup(
      id: json['id'] as String,
      name: json['name'] as String? ?? '',
      environment: json['environment'] as String? ?? 'development',
      variables: (json['variables'] as List<dynamic>? ?? [])
          .map((e) => EnvVariable.fromJson(e as Map<String, dynamic>))
          .toList(),
      createdAt: DateTime.parse(json['createdAt'] as String),
      updatedAt: DateTime.parse(json['updatedAt'] as String),
    );
  }
}
