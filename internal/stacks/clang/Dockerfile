# LLVM toolchain from Ubuntu noble, which resolves these packages to LLVM 18.
RUN apt-get update \
 && apt-get install -y --no-install-recommends clang lld lldb clangd clang-format \
 && rm -rf /var/lib/apt/lists/*
