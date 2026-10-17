"""Public, local-only inputs. No App, remote package, build script or binary target."""
SWIFT = 'public func ownedResolutionProbe() -> Int { 1 }\n'
PACKAGE = '''// swift-tools-version: 6.0
import PackageDescription
let package = Package(
    name: "LocalProbe",
    platforms: [.iOS(.v15)],
    products: [.library(name: "LocalProbe", targets: ["LocalProbe"])],
    targets: [.target(name: "LocalProbe")]
)
'''
PROJECT = '''// !$*UTF8*$!
{
 archiveVersion = 1;
 classes = {};
 objectVersion = 77;
 objects = {
  A00000000000000000000001 = {isa = PBXProject; buildConfigurationList = A00000000000000000000002; compatibilityVersion = "Xcode 16.0"; developmentRegion = en; hasScannedForEncodings = 0; knownRegions = (en, Base); mainGroup = A00000000000000000000003; productRefGroup = A00000000000000000000004; projectDirPath = ""; projectRoot = ""; packageReferences = (A00000000000000000000010); targets = (A00000000000000000000005); };
  A00000000000000000000002 = {isa = XCConfigurationList; buildConfigurations = (A00000000000000000000006); defaultConfigurationIsVisible = 0; defaultConfigurationName = Debug; };
  A00000000000000000000003 = {isa = PBXGroup; children = (A00000000000000000000004); sourceTree = "<group>"; };
  A00000000000000000000004 = {isa = PBXGroup; children = (A00000000000000000000007); name = Products; sourceTree = "<group>"; };
  A00000000000000000000005 = {isa = PBXNativeTarget; buildConfigurationList = A00000000000000000000008; buildPhases = (A00000000000000000000012); buildRules = (); dependencies = (); name = Runner; packageProductDependencies = (A00000000000000000000011); productName = Runner; productReference = A00000000000000000000007; productType = "com.apple.product-type.library.static"; };
  A00000000000000000000006 = {isa = XCBuildConfiguration; buildSettings = {SDKROOT = iphoneos; IPHONEOS_DEPLOYMENT_TARGET = 15.0; }; name = Debug; };
  A00000000000000000000007 = {isa = PBXFileReference; explicitFileType = archive.ar; includeInIndex = 0; path = libRunner.a; sourceTree = BUILT_PRODUCTS_DIR; };
  A00000000000000000000008 = {isa = XCConfigurationList; buildConfigurations = (A00000000000000000000009); defaultConfigurationIsVisible = 0; defaultConfigurationName = Debug; };
  A00000000000000000000009 = {isa = XCBuildConfiguration; buildSettings = {PRODUCT_NAME = "$(TARGET_NAME)"; SDKROOT = iphoneos; IPHONEOS_DEPLOYMENT_TARGET = 15.0; SWIFT_VERSION = 6.0; SKIP_INSTALL = YES; }; name = Debug; };
  A00000000000000000000010 = {isa = XCLocalSwiftPackageReference; relativePath = LocalProbe; };
  A00000000000000000000011 = {isa = XCSwiftPackageProductDependency; package = A00000000000000000000010; productName = LocalProbe; };
  A00000000000000000000012 = {isa = PBXFrameworksBuildPhase; buildActionMask = 2147483647; files = (A00000000000000000000013); runOnlyForDeploymentPostprocessing = 0; };
  A00000000000000000000013 = {isa = PBXBuildFile; productRef = A00000000000000000000011; };
 };
 rootObject = A00000000000000000000001;
}
'''
SCHEME = '''<?xml version="1.0" encoding="UTF-8"?>
<Scheme LastUpgradeVersion="1640" version="1.3">
 <BuildAction parallelizeBuildables="NO" buildImplicitDependencies="YES">
  <BuildActionEntries><BuildActionEntry buildForTesting="NO" buildForRunning="YES" buildForProfiling="NO" buildForArchiving="NO" buildForAnalyzing="NO">
   <BuildableReference BuildableIdentifier="primary" BlueprintIdentifier="A00000000000000000000005" BuildableName="libRunner.a" BlueprintName="Runner" ReferencedContainer="container:Runner.xcodeproj"/>
  </BuildActionEntry></BuildActionEntries>
 </BuildAction>
</Scheme>
'''


def sources():
    return {
        'source/Probe.swift': SWIFT.encode(),
        'source/app/ios/LocalProbe/Package.swift': PACKAGE.encode(),
        'source/app/ios/LocalProbe/Sources/LocalProbe/Probe.swift': SWIFT.encode(),
        'source/app/ios/Runner.xcodeproj/project.pbxproj': PROJECT.encode(),
        'source/app/ios/Runner.xcodeproj/xcshareddata/xcschemes/Runner.xcscheme': SCHEME.encode(),
    }
