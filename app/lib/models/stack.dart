class StackParameter {
  final String key;
  final String label;
  final String defaultValue;
  final bool secret;

  const StackParameter({
    required this.key,
    required this.label,
    required this.defaultValue,
    required this.secret,
  });

  factory StackParameter.fromJson(Map<String, dynamic> json) {
    return StackParameter(
      key: json['key'] as String,
      label: json['label'] as String,
      defaultValue: json['default'] as String? ?? '',
      secret: json['secret'] as bool? ?? false,
    );
  }
}

class StackSummary {
  final String id;
  final String name;
  final String description;
  final String category;
  final List<StackParameter> parameters;

  const StackSummary({
    required this.id,
    required this.name,
    required this.description,
    required this.category,
    required this.parameters,
  });

  factory StackSummary.fromJson(Map<String, dynamic> json) {
    return StackSummary(
      id: json['id'] as String,
      name: json['name'] as String,
      description: json['description'] as String? ?? '',
      category: json['category'] as String? ?? 'other',
      parameters: (json['parameters'] as List<dynamic>? ?? [])
          .map((e) => StackParameter.fromJson(e as Map<String, dynamic>))
          .toList(),
    );
  }
}
