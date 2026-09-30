package com.tripfolio.app;

import android.os.Bundle;
import com.getcapacitor.BridgeActivity;

public class MainActivity extends BridgeActivity {
    @Override
    public void onCreate(Bundle savedInstanceState) {
        registerPlugin(LedgerTemplatePlugin.class);
        super.onCreate(savedInstanceState);
    }
}
