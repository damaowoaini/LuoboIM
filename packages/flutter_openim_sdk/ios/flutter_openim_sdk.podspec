#
# To learn more about a Podspec see http://guides.cocoapods.org/syntax/podspec.html.
# Run `pod lib lint flutter_openim_sdk.podspec` to validate before publishing.
#
Pod::Spec.new do |s|
  s.name             = 'flutter_openim_sdk'
  s.version          = '0.0.1'
  s.summary          = 'Customized OpenIM bridge for Luobo Duihua.'
  s.description      = <<-DESC
OpenIM Flutter bridge, preserving the existing customized native core protocol.
                       DESC
  s.homepage         = 'https://github.com/OpenIMSDK/Open-IM-SDK-Flutter'
  s.license          = { :file => '../LICENSE' }
  s.author           = 'OpenIM contributors'
  s.source           = { :path => '.' }
  s.source_files = 'Classes/**/*'
  s.dependency 'Flutter'
  s.platform = :ios, '13.0'

  s.static_framework = true
  s.vendored_frameworks = 'Framework/*.xcframework'
  # The supplied XCFramework contains arm64 simulator and device slices.
  s.pod_target_xcconfig = { 'DEFINES_MODULE' => 'YES' }
  s.swift_version = '5.0'
end
