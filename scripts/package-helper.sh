#!/bin/bash

# package_helper.sh
add_includes_to_package() {
    echo "Adding includes to package: $1"
    temp_dir=$(mktemp -d)
    7z x "$1" "-o$temp_dir"
    for browser_dir in vulpine camoufox; do
        if [ -d "$temp_dir/$browser_dir" ]; then
            mv "$temp_dir/$browser_dir"/* "$temp_dir/"
            rmdir "$temp_dir/$browser_dir"
        fi
    done
    for include in "${@:2}"; do
        if [ -e "$include" ]; then
            cp -r "$include" "$temp_dir/"
        fi
    done
    (cd "$temp_dir" && 7z u "../$1" ./* -r -tzip -mx=9)
    rm -rf "$temp_dir"
}

# Execute the function with all arguments passed to the script
add_includes_to_package "$@"
