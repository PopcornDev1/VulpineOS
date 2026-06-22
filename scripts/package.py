#!/usr/bin/env python3

import argparse
import glob
import os
import plistlib
import shutil
import subprocess
import sys
import tempfile
from shlex import join

from _mixin import find_src_dir, get_moz_target, list_files, run, temp_cd

UNNEEDED_PATHS = {'uninstall', 'pingsender.exe', 'pingsender', 'vaapitest', 'glxtest'}
APP_BUNDLE_NAMES = ('Vulpine.app', 'Camoufox.app')
BROWSER_PACKAGE_PREFIXES = ('vulpine', 'camoufox')
MACOS_HELPER_EXECUTABLES = (
    ('gpu-helper.app', 'Camoufox GPU Helper', 'Vulpine GPU Helper'),
    ('media-plugin-helper.app', 'Camoufox Media Plugin Helper', 'Vulpine Media Plugin Helper'),
)


def extract_macos_dmg(package_file, temp_dir):
    """Extract a macOS DMG using native tooling so app metadata is preserved."""
    mount_dir = os.path.join(temp_dir, 'mount')
    os.makedirs(mount_dir, exist_ok=True)
    subprocess.run(
        ['hdiutil', 'attach', '-nobrowse', '-readonly', '-mountpoint', mount_dir, package_file],
        check=True,
    )
    try:
        app_candidates = [
            os.path.join(mount_dir, 'Vulpine.app'),
            os.path.join(mount_dir, 'Vulpine', 'Vulpine.app'),
            os.path.join(mount_dir, 'Camoufox.app'),
            os.path.join(mount_dir, 'Camoufox', 'Camoufox.app'),
        ]
        app_path = next((path for path in app_candidates if os.path.exists(path)), None)
        if not app_path:
            raise FileNotFoundError(f'Vulpine.app/Camoufox.app not found in mounted DMG: {package_file}')
        shutil.copytree(app_path, os.path.join(temp_dir, 'Vulpine.app'), symlinks=True)
    finally:
        subprocess.run(['hdiutil', 'detach', mount_dir], check=False)
        shutil.rmtree(mount_dir, ignore_errors=True)


def create_macos_zip(app_path, new_file):
    """Create a macOS app zip with native resource-fork handling."""
    subprocess.run(
        ['ditto', '-c', '-k', '--sequesterRsrc', '--keepParent', app_path, new_file],
        check=True,
    )


def normalize_macos_app_bundle(app_path):
    """Normalize a browser app bundle to the public Vulpine app identity."""
    macos_dir = os.path.join(app_path, 'Contents', 'MacOS')
    old_binary = os.path.join(macos_dir, 'camoufox')
    new_binary = os.path.join(macos_dir, 'vulpine')
    if os.path.exists(old_binary) and not os.path.exists(new_binary):
        os.rename(old_binary, new_binary)

    plist_path = os.path.join(app_path, 'Contents', 'Info.plist')
    if os.path.exists(plist_path):
        with open(plist_path, 'rb') as f:
            plist = plistlib.load(f)
        if os.path.exists(new_binary):
            plist['CFBundleExecutable'] = 'vulpine'
        plist['CFBundleName'] = 'Vulpine'
        plist['CFBundleDisplayName'] = 'Vulpine'
        with open(plist_path, 'wb') as f:
            plistlib.dump(plist, f)

    for helper_bundle, old_name, new_name in MACOS_HELPER_EXECUTABLES:
        helper_macos_dir = os.path.join(macos_dir, helper_bundle, 'Contents', 'MacOS')
        old_helper = os.path.join(helper_macos_dir, old_name)
        new_helper = os.path.join(helper_macos_dir, new_name)
        if os.path.exists(old_helper) and not os.path.exists(new_helper):
            os.rename(old_helper, new_helper)
        elif os.path.exists(old_helper):
            os.remove(old_helper)

        helper_plist_path = os.path.join(macos_dir, helper_bundle, 'Contents', 'Info.plist')
        if os.path.exists(helper_plist_path):
            with open(helper_plist_path, 'rb') as f:
                helper_plist = plistlib.load(f)
            if os.path.exists(new_helper):
                helper_plist['CFBundleExecutable'] = new_name
            with open(helper_plist_path, 'wb') as f:
                plistlib.dump(helper_plist, f)


def normalize_flat_browser_names(target_dir):
    """Expose Vulpine executable names when rewrapping older flat browser archives."""
    for old_name, new_name in (('camoufox', 'vulpine'), ('camoufox-bin', 'vulpine-bin')):
        old_path = os.path.join(target_dir, old_name)
        new_path = os.path.join(target_dir, new_name)
        if os.path.exists(old_path) and not os.path.exists(new_path):
            shutil.copy2(old_path, new_path)


def add_includes_to_package(package_file, includes, fonts, new_file, target):
    new_file = os.path.abspath(new_file)
    with tempfile.TemporaryDirectory() as temp_dir:
        use_native_macos = (
            target == 'macos'
            and package_file.endswith('.dmg')
            and shutil.which('hdiutil') is not None
            and shutil.which('ditto') is not None
        )

        # Extract package
        if use_native_macos:
            extract_macos_dmg(package_file, temp_dir)
        else:
            if shutil.which('7z') is None:
                print('Error: 7z is required for packaging this target. Install p7zip and retry.')
                sys.exit(1)
            if run(join(['7z', 'x', package_file, f'-o{temp_dir}']), exit_on_fail=False) != 0:
                print(f'Error: failed to extract package: {package_file}')
                sys.exit(1)

        # Delete package_file
        os.remove(package_file)
        if package_file.endswith('.tar.xz'):
            # Rerun on the tar file
            package_tar = package_file[:-3]  # Remove ".xz"
            return add_includes_to_package(
                package_file=os.path.join(temp_dir, package_tar),
                includes=includes,
                fonts=fonts,
                new_file=new_file,
                target=target,
            )

        if target == 'macos':
            # Move nested app bundles to a stable Vulpine.app output.
            app_path = os.path.join(temp_dir, 'Vulpine.app')
            for bundle_name in APP_BUNDLE_NAMES:
                nested_dir = os.path.join(temp_dir, os.path.splitext(bundle_name)[0])
                nested_app_path = os.path.join(nested_dir, bundle_name)
                flat_app_path = os.path.join(temp_dir, bundle_name)
                if os.path.exists(nested_app_path):
                    if os.path.exists(app_path):
                        shutil.rmtree(app_path)
                    shutil.move(nested_app_path, app_path)
                    shutil.rmtree(nested_dir)
                    break
                if bundle_name != 'Vulpine.app' and os.path.exists(flat_app_path):
                    if os.path.exists(app_path):
                        shutil.rmtree(app_path)
                    shutil.move(flat_app_path, app_path)
                    break
            else:
                if not os.path.exists(app_path):
                    raise FileNotFoundError(f'Vulpine.app/Camoufox.app not found after extracting {package_file}')
            normalize_macos_app_bundle(app_path)
        else:
            # Move contents out of a top-level browser folder if it exists.
            for prefix in BROWSER_PACKAGE_PREFIXES:
                old_browser_dir = os.path.join(temp_dir, prefix)
                browser_dir = os.path.join(temp_dir, f'{prefix}-folder')
                if os.path.exists(old_browser_dir):
                    os.rename(old_browser_dir, browser_dir)
                    for item in os.listdir(browser_dir):
                        shutil.move(os.path.join(browser_dir, item), temp_dir)
                    os.rmdir(browser_dir)
            normalize_flat_browser_names(temp_dir)

        # Create target_dir
        if target == 'macos':
            target_dir = os.path.join(temp_dir, 'Vulpine.app', 'Contents', 'Resources')
        else:
            target_dir = temp_dir

        # Add includes
        for include in includes or []:
            if os.path.exists(include):
                if os.path.isdir(include):
                    shutil.copytree(
                        include,
                        os.path.join(target_dir, os.path.basename(include)),
                        dirs_exist_ok=True,
                    )
                else:
                    shutil.copy2(include, target_dir)

        # Add the font folders under fonts/
        fonts_dir = os.path.join(target_dir, 'fonts')
        if target == 'linux':
            for font in fonts or []:
                shutil.copytree(
                    os.path.join('bundle', 'fonts', font),
                    os.path.join(fonts_dir, font),
                    dirs_exist_ok=True,
                )
        # Non-linux systems cannot read fonts within subfolders.
        # Instead, we walk the fonts/ directory and copy all files.
        else:
            os.makedirs(fonts_dir, exist_ok=True)
            for font in fonts or []:
                for file in list_files(root_dir=os.path.join('bundle', 'fonts', font), suffix='*'):
                    shutil.copy2(file, os.path.join(fonts_dir, os.path.basename(file)))

        # Remove unneeded paths
        for path in UNNEEDED_PATHS:
            if os.path.isdir(os.path.join(target_dir, path)):
                shutil.rmtree(os.path.join(target_dir, path), ignore_errors=True)
            elif os.path.exists(os.path.join(target_dir, path)):
                os.remove(os.path.join(target_dir, path))

        # Update package
        if use_native_macos:
            create_macos_zip(os.path.join(temp_dir, 'Vulpine.app'), new_file)
        else:
            run(join(['7z', 'u', new_file, f'{temp_dir}/*', '-r', '-mx=9']))


def get_args():
    """Get CLI parameters"""
    parser = argparse.ArgumentParser(
        description='Package Vulpine for different operating systems.'
    )
    parser.add_argument('os', choices=['linux', 'macos', 'windows'], help='Target operating system')
    parser.add_argument(
        '--includes', nargs='+', help='List of files or directories to include in the package'
    )
    parser.add_argument('--version', required=True, help='Vulpine browser version')
    parser.add_argument('--release', required=True, help='Vulpine browser release number')
    parser.add_argument(
        '--arch', choices=['x86_64', 'i686', 'arm64'], help='Architecture for Windows build'
    )
    parser.add_argument('--fonts', nargs='+', help='Font directories to include under fonts/')
    return parser.parse_args()


def main():
    """The main packaging function"""
    args = get_args()

    # Determine file extension based on OS
    file_extensions = {'linux': 'tar.xz', 'macos': 'dmg', 'windows': 'zip'}
    file_ext = file_extensions[args.os]

    # Build the package
    src_dir = find_src_dir('.', args.version, args.release)
    moz_target = get_moz_target(target=args.os, arch=args.arch)
    with temp_cd(src_dir):
        # Create package files
        run('./mach package')
        # Find package files
        search_patterns = [
            os.path.abspath(
                f'obj-{moz_target}/dist/{prefix}-{args.version}-{args.release}.*.{file_ext}'
            )
            for prefix in BROWSER_PACKAGE_PREFIXES
        ]

    # Copy package files
    package_file = None
    for search_path in search_patterns:
        for file in glob.glob(search_path):
            if 'xpt_artifacts' in file:
                print(f'Skipping xpt artifacts: {file}')
                continue
            print(f'Found package: {file}')
            shutil.copy2(file, '.')
            package_file = os.path.basename(file)
            break
        if package_file:
            break
    if not package_file:
        print(f"Error: No package file found matching patterns: {', '.join(search_patterns)}")
        sys.exit(1)

    # Find the package file
    package_files = []
    for prefix in BROWSER_PACKAGE_PREFIXES:
        package_files.extend(glob.glob(f'{prefix}-{args.version}-{args.release}.en-US.*.{file_ext}'))
    if not package_files:
        print(f"Error: No package file found for Vulpine/Camoufox {args.version}-{args.release}")
        sys.exit(1)
    package_file = package_files[0]

    # Add includes to the package
    new_name = f'vulpine-{args.version}-{args.release}-{args.os[:3]}.{args.arch}.zip'
    add_includes_to_package(
        package_file=package_file,
        includes=args.includes,
        fonts=args.fonts,
        new_file=new_name,
        target=args.os,
    )

    print(f"Packaging complete for {args.os}")


if __name__ == '__main__':
    main()
