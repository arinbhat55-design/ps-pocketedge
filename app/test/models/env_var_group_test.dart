import 'package:app/models/env_var_group.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  group('EnvVariable', () {
    test('fromJson maps "default" to value', () {
      final v = EnvVariable.fromJson({
        'key': 'DB_PASSWORD',
        'label': 'Database password',
        'default': 's3cr3t',
        'secret': true,
      });

      expect(v.key, 'DB_PASSWORD');
      expect(v.label, 'Database password');
      expect(v.value, 's3cr3t');
      expect(v.secret, isTrue);
    });

    test('toJson round-trips through fromJson', () {
      const v = EnvVariable(
        key: 'FOO',
        label: 'Foo',
        value: 'bar',
        secret: false,
      );
      final roundTripped = EnvVariable.fromJson(v.toJson());

      expect(roundTripped.key, v.key);
      expect(roundTripped.label, v.label);
      expect(roundTripped.value, v.value);
      expect(roundTripped.secret, v.secret);
    });

    test('defaults are safe when fields are absent', () {
      final v = EnvVariable.fromJson({'key': 'FOO'});
      expect(v.label, '');
      expect(v.value, '');
      expect(v.secret, isFalse);
    });
  });

  group('EnvVarGroup.fromJson', () {
    test('parses a fully populated group', () {
      final group = EnvVarGroup.fromJson({
        'id': 'g1',
        'name': 'Production database',
        'environment': 'production',
        'variables': [
          {'key': 'DB_HOST', 'default': 'db.internal', 'secret': false},
        ],
        'createdAt': '2026-01-01T00:00:00Z',
        'updatedAt': '2026-01-02T00:00:00Z',
      });

      expect(group.id, 'g1');
      expect(group.environment, 'production');
      expect(group.variables, hasLength(1));
      expect(group.variables.first.value, 'db.internal');
    });

    test('defaults environment to development when absent', () {
      final group = EnvVarGroup.fromJson({
        'id': 'g1',
        'name': 'x',
        'createdAt': '2026-01-01T00:00:00Z',
        'updatedAt': '2026-01-01T00:00:00Z',
      });
      expect(group.environment, 'development');
      expect(group.variables, isEmpty);
    });
  });

  test('kEnvironments lists all four profiles', () {
    expect(kEnvironments, [
      'development',
      'test',
      'staging',
      'production',
    ]);
  });
}
