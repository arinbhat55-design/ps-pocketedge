import 'package:app/main.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  const channel = MethodChannel('com.pspocketedge.app/config');
  final messenger =
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
  tearDown(() => messenger.setMockMethodCallHandler(channel, null));

  test('installer API address overrides the compiled default', () async {
    messenger.setMockMethodCallHandler(channel, (call) async {
      expect(call.method, 'getControlPlaneURL');
      return 'https://control.example.com';
    });
    expect(await configuredControlPlaneUrl(), 'https://control.example.com');
  });
  test('other platforms retain their compiled API address', () async {
    expect(await configuredControlPlaneUrl(), controlPlaneUrl);
  });
  test('invalid or credential-bearing preferences fall back safely', () async {
    for (final address in [
      'file:///tmp/config',
      'https://user:password@host',
      'broken',
      'https://host?secret=x',
    ]) {
      messenger.setMockMethodCallHandler(channel, (_) async => address);
      expect(await configuredControlPlaneUrl(), controlPlaneUrl);
    }
  });
}
