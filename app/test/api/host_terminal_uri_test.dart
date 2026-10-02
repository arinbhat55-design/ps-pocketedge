import 'package:app/api/api_client.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test(
    'host terminal URI selects the host and retains authentication and size',
    () {
      final client = ApiClient(
        baseUrl: 'https://control.example',
        authToken: 'test-token',
      );
      final uri = client.hostExecUri('host-1', cols: 120, rows: 40);
      expect(uri.scheme, 'wss');
      expect(uri.path, '/api/servers/host-1/exec');
      expect(uri.queryParameters, {
        'token': 'test-token',
        'cols': '120',
        'rows': '40',
      });
      expect(
        client.containerExecUri('host-1', 'container-1').path,
        '/api/servers/host-1/containers/container-1/exec',
      );
    },
  );
}
