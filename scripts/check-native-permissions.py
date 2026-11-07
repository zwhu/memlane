#!/usr/bin/env python3
"""Read macOS native test permissions without requesting or changing them."""
import argparse
import ctypes
import json
from pathlib import Path


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--out',type=Path)
    args=parser.parse_args()
    ax=ctypes.CDLL('/System/Library/Frameworks/ApplicationServices.framework/ApplicationServices')
    ax.AXIsProcessTrusted.restype=ctypes.c_bool
    cg=ctypes.CDLL('/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics')
    result={'accessibility_trusted':ax.AXIsProcessTrusted()}
    for name in ['CGPreflightPostEventAccess','CGPreflightScreenCaptureAccess','CGPreflightListenEventAccess']:
        try:
            probe=getattr(cg,name)
            probe.restype=ctypes.c_bool
            result[name]=probe()
        except AttributeError:
            result[name]='unavailable'
    if args.out:
        args.out.write_text(json.dumps(result,indent=2)+'\n')
    print(json.dumps(result))


if __name__=='__main__':main()
