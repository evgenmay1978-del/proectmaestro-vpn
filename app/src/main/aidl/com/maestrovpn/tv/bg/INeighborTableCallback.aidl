package com.maestrovpn.tv.bg;

import com.maestrovpn.tv.bg.ParceledListSlice;

interface INeighborTableCallback {
    oneway void onNeighborTableUpdated(in ParceledListSlice entries);
}
