import Foundation


public class CommonUtil {
    
    public static func emitEvent(channel: FlutterMethodChannel, method: String, type: String, errCode: Int32?, errMsg: String?, data: Any?){
        safeMainAsync {
            var res: [String: Any] = [:]
            res["type"] = type
            res["data"] = data
            res["errCode"] = errCode
            res["errMsg"] = errMsg
            channel.invokeMethod(method, arguments: res)
        }
    }
}
