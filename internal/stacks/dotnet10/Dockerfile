# Runtime libraries the dotnet SDK needs on Ubuntu (ICU, OpenSSL, zlib, Kerberos,
# tzdata). Identical in dotnet8 and dotnet10 so selecting both is harmless.
RUN apt-get update \
    && apt-get install -y --no-install-recommends curl ca-certificates libicu74 libssl3t64 zlib1g libgssapi-krb5-2 tzdata \
    && rm -rf /var/lib/apt/lists/*

# Microsoft's dotnet-install.sh gives arch-neutral installs and lets several SDK
# channels coexist in one directory.
ARG DOTNET10_CHANNEL=10.0
RUN curl -fsSL https://dot.net/v1/dotnet-install.sh -o /tmp/dotnet-install.sh \
    && bash /tmp/dotnet-install.sh --channel "${DOTNET10_CHANNEL}" --install-dir /usr/share/dotnet --no-path \
    && rm -f /tmp/dotnet-install.sh \
    && ln -sf /usr/share/dotnet/dotnet /usr/local/bin/dotnet

# NUGET_PACKAGES points at the aide cache volume (cache: nuget); the opt-outs keep
# first-run banners, telemetry and dev-cert generation out of the container.
ENV DOTNET_ROOT=/usr/share/dotnet DOTNET_NOLOGO=1 DOTNET_CLI_TELEMETRY_OPTOUT=1 DOTNET_GENERATE_ASPNET_CERTIFICATE=false \
    DOTNET_RUNNING_IN_CONTAINER=true DOTNET_USE_POLLING_FILE_WATCHER=true \
    NUGET_PACKAGES=/var/cache/aide/nuget

# Fail the build early if the SDK did not install.
RUN dotnet --list-sdks
