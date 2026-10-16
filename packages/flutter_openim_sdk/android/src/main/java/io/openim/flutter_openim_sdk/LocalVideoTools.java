package io.openim.flutter_openim_sdk;

import android.content.Context;
import android.graphics.Bitmap;
import android.media.MediaMetadataRetriever;
import android.os.Build;
import android.os.Handler;
import android.os.Looper;
import java.io.File;
import java.io.FileOutputStream;
import java.util.HashMap;
import java.util.Map;
import io.flutter.plugin.common.MethodCall;
import io.flutter.plugin.common.MethodChannel;

/** Local media utility; no OpenIM core or network call is involved. */
final class LocalVideoTools {
    static void inspect(Context context, MethodCall call, MethodChannel.Result result) {
        final String source = call.argument("videoPath");
        final String destination = call.argument("thumbnailPath");
        new Thread(() -> {
            final Handler main = new Handler(Looper.getMainLooper());
            MediaMetadataRetriever retriever = new MediaMetadataRetriever();
            Bitmap frame = null;
            try {
                if (source == null || destination == null) throw new IllegalArgumentException();
                File video = new File(source).getCanonicalFile();
                File thumbnail = new File(destination).getCanonicalFile();
                File container = new File(context.getApplicationInfo().dataDir).getCanonicalFile();
                if (!video.isFile() || !thumbnail.getPath().startsWith(container.getPath() + File.separator)) {
                    throw new IllegalArgumentException();
                }
                File parent = thumbnail.getParentFile();
                if (parent == null || (!parent.isDirectory() && !parent.mkdirs())) throw new IllegalArgumentException();
                retriever.setDataSource(video.getPath());
                long duration = Long.parseLong(retriever.extractMetadata(MediaMetadataRetriever.METADATA_KEY_DURATION));
                int width = Integer.parseInt(retriever.extractMetadata(MediaMetadataRetriever.METADATA_KEY_VIDEO_WIDTH));
                int height = Integer.parseInt(retriever.extractMetadata(MediaMetadataRetriever.METADATA_KEY_VIDEO_HEIGHT));
                if (duration <= 0 || width <= 0 || height <= 0) throw new IllegalArgumentException();
                if (Build.VERSION.SDK_INT >= 27) {
                    double scale = Math.min(1.0, 720.0 / Math.max(width, height));
                    frame = retriever.getScaledFrameAtTime(0, MediaMetadataRetriever.OPTION_CLOSEST_SYNC,
                            Math.max(1, (int) (width * scale)), Math.max(1, (int) (height * scale)));
                } else {
                    frame = retriever.getFrameAtTime(0, MediaMetadataRetriever.OPTION_CLOSEST_SYNC);
                }
                if (frame == null) throw new IllegalArgumentException();
                try (FileOutputStream stream = new FileOutputStream(thumbnail)) {
                    if (!frame.compress(Bitmap.CompressFormat.JPEG, 80, stream)) throw new IllegalArgumentException();
                }
                final Map<String, Object> metadata = new HashMap<>();
                metadata.put("durationMilliseconds", duration);
                metadata.put("width", width);
                metadata.put("height", height);
                // Preserve the caller's app-container alias after canonical
                // validation (/data/user/0 and /data/data can be equivalent).
                metadata.put("thumbnailPath", destination);
                main.post(() -> result.success(metadata));
            } catch (Exception | OutOfMemoryError error) {
                main.post(() -> result.error("media_invalid", "Unable to process this video", null));
            } finally {
                if (frame != null) frame.recycle();
                try { retriever.release(); } catch (Exception ignored) {}
            }
        }, "luobo-local-video").start();
    }
}
