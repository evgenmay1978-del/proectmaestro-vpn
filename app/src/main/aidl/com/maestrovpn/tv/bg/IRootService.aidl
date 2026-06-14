package com.maestrovpn.tv.bg;

import android.os.ParcelFileDescriptor;
import com.maestrovpn.tv.bg.INeighborTableCallback;
import com.maestrovpn.tv.bg.IRootShellSession;
import com.maestrovpn.tv.bg.ParceledListSlice;

interface IRootService {
    void destroy() = 16777114; // Destroy method defined by Shizuku server

    ParceledListSlice getInstalledPackages(int flags, int userId) = 1;

    void installPackage(in ParcelFileDescriptor apk, long size, int userId) = 2;

    String exportDebugInfo(String outputPath) = 3;

    void registerNeighborTableCallback(in INeighborTableCallback callback) = 4;

    oneway void unregisterNeighborTableCallback(in INeighborTableCallback callback) = 5;

    IRootShellSession openShellSession(String user, String command, in String[] env, String term, int rows, int cols) = 6;

    String lookupSFTPServer() = 7;
}
