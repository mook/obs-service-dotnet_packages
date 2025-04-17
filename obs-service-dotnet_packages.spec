#
# spec file for package obs-service-dotnet_packages
#
# Copyright (c) 2025 Mook
#
# All modifications and additions to the file contributed by third parties
# remain the property of their copyright owners, unless otherwise agreed
# upon. The license for this file, and modifications and additions to the
# file, is the same license as for the pristine package itself (unless the
# license for the pristine package is not an Open Source License, in which
# case the license is the MIT License). An "Open Source License" is a
# license that conforms to the Open Source Definition (Version 1.9)
# published by the Open Source Initiative.

# Please submit bugfixes or comments via the GitHub repository.

%define service dotnet_packages
Name:           obs-service-%{service}
Version:        0
Release:        1%{?dist}
Summary:        OBS source service to vendor dotnet packages
License:        AGPL-3.0-or-later
Group:          Development/Tools/Building
URL:            https://github.com/mook/obs-service-%{service}
Source:         %{name}-%{version}.tar.zst
Source1:        vendor.tar.zst
BuildRequires:  golang-packaging
# Needed for go -mod=vendor
BuildRequires:  golang(API) >= 1.24
BuildRequires:  zstd
Requires:       docker

%description
An OBS Source Service that will download and vendor dependencies of dotnet
applications from NuGet into an archive.

This is done by running `dotnet restore` inside a docker container.

Note that because NuGet provides binaries, not sources, the outputs of this
source service is not appropriate for packages intended to be submitted into
openSUSE Factory.

%prep
%autosetup -p1 -a1

%build
go build -mod=vendor -buildmode=pie

%install
mkdir -p %{buildroot}%{_prefix}/lib/obs/service
install -m 0755 %{name} %{buildroot}%{_prefix}/lib/obs/service/%{service}
install -m 0644 %{name}.service %{buildroot}%{_prefix}/lib/obs/service/%{service}.service

%files
%license LICENSE
%dir %{_prefix}/lib/obs
%{_prefix}/lib/obs/service

%changelog
