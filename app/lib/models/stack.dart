class StackSummary {
  final String id;
  final String name;

  const StackSummary({required this.id, required this.name});

  factory StackSummary.fromJson(Map<String, dynamic> json) {
    return StackSummary(
      id: json['id'] as String,
      name: json['name'] as String,
    );
  }
}
