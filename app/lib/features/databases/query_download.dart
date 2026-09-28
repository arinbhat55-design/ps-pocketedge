export 'query_download_stub.dart'
    if (dart.library.io) 'query_download_io.dart'
    if (dart.library.html) 'query_download_web.dart';
