package com.tripfolio.app;

import android.app.Activity;
import android.content.Intent;
import android.util.Base64;
import androidx.activity.result.ActivityResult;
import com.getcapacitor.JSObject;
import com.getcapacitor.Plugin;
import com.getcapacitor.PluginCall;
import com.getcapacitor.PluginMethod;
import com.getcapacitor.annotation.ActivityCallback;
import com.getcapacitor.annotation.CapacitorPlugin;
import java.io.OutputStream;

@CapacitorPlugin(name = "LedgerTemplate")
public class LedgerTemplatePlugin extends Plugin {
    @PluginMethod
    public void save(PluginCall call) {
        String data = call.getString("data");
        if (data == null || data.length() > 8 * 1024 * 1024) {
            call.reject("模板数据无效");
            return;
        }
        Intent intent = new Intent(Intent.ACTION_CREATE_DOCUMENT);
        intent.addCategory(Intent.CATEGORY_OPENABLE);
        intent.setType("application/vnd.openxmlformats-officedocument.spreadsheetml.sheet");
        intent.putExtra(Intent.EXTRA_TITLE, "Tripfolio-账单导入模板.xlsx");
        try {
            startActivityForResult(call, intent, "saved");
        } catch (Exception error) {
            call.reject("无法打开系统文件保存窗口", error);
        }
    }

    @ActivityCallback
    private void saved(PluginCall call, ActivityResult result) {
        if (call == null) return;
        if (result.getResultCode() != Activity.RESULT_OK || result.getData() == null || result.getData().getData() == null) {
            call.resolve(new JSObject().put("saved", false));
            return;
        }
        try (OutputStream output = getContext().getContentResolver().openOutputStream(result.getData().getData(), "wt")) {
            if (output == null) {
                call.reject("无法写入所选文件");
                return;
            }
            output.write(Base64.decode(call.getString("data", ""), Base64.DEFAULT));
            output.flush();
            call.resolve(new JSObject().put("saved", true));
        } catch (Exception error) {
            call.reject("保存模板失败", error);
        }
    }
}
