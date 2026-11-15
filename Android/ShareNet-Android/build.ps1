$ErrorActionPreference = 'Stop'

$javacInPath = (Get-Command javac.exe -ErrorAction SilentlyContinue).Source
$pathJdk = if ($javacInPath) { Split-Path (Split-Path $javacInPath -Parent) -Parent } else { $null }
$jdkCandidates = @(
    $env:JAVA_HOME,
    $pathJdk,
    "C:\Program Files\Android\Android Studio\jbr",
    "C:\Program Files (x86)\Android\openjdk\jdk-17.0.8.101-hotspot"
) + @(
    (Get-ChildItem "C:\Program Files\Java\jdk*", "C:\Program Files\Eclipse Adoptium\jdk*", "C:\Program Files\Microsoft\jdk*", "D:\*\*\jdk*", "D:\*\*\*\jdk*" -ErrorAction SilentlyContinue | ForEach-Object { $_.FullName })
) | Where-Object {
    $_ -and (Test-Path (Join-Path $_ "bin\javac.exe")) -and (Test-Path (Join-Path $_ "bin\jar.exe")) -and (Test-Path (Join-Path $_ "bin\keytool.exe"))
}
$jdkHome = $jdkCandidates | Select-Object -First 1
if (-not $jdkHome) { throw "JDK (javac.exe) not found" }

$sdkCandidates = @(
    "C:\Program Files (x86)\Android\android-sdk",
    $env:ANDROID_HOME,
    $env:ANDROID_SDK_ROOT,
    "$env:LOCALAPPDATA\Android\Sdk"
) | Where-Object { $_ -and (Test-Path (Join-Path $_ "build-tools")) }
$sdkHome = $sdkCandidates | Select-Object -First 1
if (-not $sdkHome) { throw "Android SDK not found" }
$btCandidates = @("35.0.1", "34.0.0") | ForEach-Object { Join-Path $sdkHome "build-tools\$_" } | Where-Object { Test-Path (Join-Path $_ "aapt2.exe") }
$buildTools = $btCandidates | Select-Object -First 1
if (-not $buildTools) { throw "Android build-tools (35.0.1 or 34.0.0) not found" }
$androidJar = Join-Path $sdkHome "platforms\android-34\android.jar"

$javac = Join-Path $jdkHome "bin\javac.exe"
$jar = Join-Path $jdkHome "bin\jar.exe"
$keytool = Join-Path $jdkHome "bin\keytool.exe"
$aapt2 = Join-Path $buildTools "aapt2.exe"
$d8 = Join-Path $buildTools "d8.bat"
$zipalign = Join-Path $buildTools "zipalign.exe"
$apksigner = Join-Path $buildTools "apksigner.bat"

$env:JAVA_HOME = $jdkHome
$env:PATH = "$jdkHome\bin;$env:PATH"

$srcRoot = $PSScriptRoot
$workRoot = $srcRoot
if ($srcRoot -match '[^\x00-\x7F]') {
    $workRoot = Join-Path $env:TEMP "hotspot-proxy-build"
    if (Test-Path $workRoot) { Remove-Item $workRoot -Recurse -Force }
    New-Item -ItemType Directory -Force -Path $workRoot | Out-Null
    Copy-Item (Join-Path $srcRoot "res") (Join-Path $workRoot "res") -Recurse -Force
    Copy-Item (Join-Path $srcRoot "src") (Join-Path $workRoot "src") -Recurse -Force
    Copy-Item (Join-Path $srcRoot "AndroidManifest.xml") (Join-Path $workRoot "AndroidManifest.xml") -Force
    if (Test-Path (Join-Path $srcRoot "libs")) {
        Copy-Item (Join-Path $srcRoot "libs") (Join-Path $workRoot "libs") -Recurse -Force
    }
    if (Test-Path (Join-Path $srcRoot "jniLibs")) {
        Copy-Item (Join-Path $srcRoot "jniLibs") (Join-Path $workRoot "jniLibs") -Recurse -Force
    }
    if (Test-Path (Join-Path $srcRoot "debug.keystore")) {
        Copy-Item (Join-Path $srcRoot "debug.keystore") (Join-Path $workRoot "debug.keystore") -Force
    }
}

$root = $workRoot
$buildDir = Join-Path $root "build"
$genDir = Join-Path $buildDir "gen"
$objDir = Join-Path $buildDir "obj"
$dexDir = Join-Path $buildDir "dex"
$resFlatDir = Join-Path $buildDir "res_flat"

if (Test-Path $buildDir) { Remove-Item $buildDir -Recurse -Force }
New-Item -ItemType Directory -Force -Path $genDir, $objDir, $dexDir, $resFlatDir | Out-Null

Write-Host "1/6 Compiling resources..."
& $aapt2 compile --dir (Join-Path $root "res") -o $resFlatDir
if ($LASTEXITCODE -ne 0) { throw "aapt2 compile failed" }

$flatFiles = @(Get-ChildItem $resFlatDir -Filter "*.flat" | ForEach-Object { $_.FullName })
$unalignedApk = Join-Path $buildDir "hotspot-proxy-unaligned.apk"

Write-Host "2/6 Linking APK..."
$linkArgs = @(
    "link",
    "-o", $unalignedApk,
    "-I", $androidJar,
    "--manifest", (Join-Path $root "AndroidManifest.xml"),
    "--java", $genDir
) + $flatFiles
& $aapt2 @linkArgs
if ($LASTEXITCODE -ne 0) { throw "aapt2 link failed" }

$extraJars = @()
$libsDir = Join-Path $root "libs"
if (Test-Path $libsDir) {
    $extraJars = @(Get-ChildItem $libsDir -Filter "*.jar" | ForEach-Object { $_.FullName })
}
$javacClasspath = (@($androidJar) + $extraJars) -join ";"

Write-Host "3/6 Compiling Java..."
$javaFiles = @(Get-ChildItem (Join-Path $root "src"), $genDir -Recurse -Filter "*.java" | ForEach-Object { $_.FullName })
& $javac -encoding UTF-8 --release 11 -classpath $javacClasspath -d $objDir @javaFiles
if ($LASTEXITCODE -ne 0) { throw "javac failed" }

Write-Host "4/6 Converting to DEX..."
$classFiles = @(Get-ChildItem $objDir -Recurse -Filter "*.class" | ForEach-Object { $_.FullName })
$d8Inputs = $classFiles + $extraJars
& $d8 --min-api 24 --lib $androidJar --output $dexDir @d8Inputs
if ($LASTEXITCODE -ne 0) { throw "d8 failed" }

Write-Host "5/6 Packing classes.dex, native libs and aligning..."
& $jar -uf $unalignedApk -C $dexDir classes.dex
if ($LASTEXITCODE -ne 0) { throw "jar update failed" }

$jniDir = Join-Path $root "jniLibs"
if (Test-Path $jniDir) {
    $libStage = Join-Path $buildDir "lib_stage"
    $libSub = Join-Path $libStage "lib"
    if (Test-Path $libStage) { Remove-Item $libStage -Recurse -Force }
    New-Item -ItemType Directory -Force -Path $libSub | Out-Null
    Copy-Item (Join-Path $jniDir "*") $libSub -Recurse -Force
    & $jar -uf $unalignedApk -C $libStage lib
    if ($LASTEXITCODE -ne 0) { throw "jar update native libs failed" }
}

$alignedApk = Join-Path $buildDir "hotspot-proxy-aligned.apk"
& $zipalign -f -p 4 $unalignedApk $alignedApk
if ($LASTEXITCODE -ne 0) { throw "zipalign failed" }

$keystore = Join-Path $root "debug.keystore"
if (-not (Test-Path $keystore)) {
    & $keytool -genkeypair -v -keystore $keystore -storepass android -alias androiddebugkey -keypass android -keyalg RSA -keysize 2048 -validity 10000 -dname "CN=Android Debug,O=Android,C=US"
    if ($LASTEXITCODE -ne 0) { throw "keytool failed" }
}

$finalApk = Join-Path $buildDir "hotspot-proxy.apk"
Write-Host "6/6 Signing APK..."
& $apksigner sign --ks $keystore --ks-pass pass:android --key-pass pass:android --out $finalApk $alignedApk
if ($LASTEXITCODE -ne 0) { throw "apksigner failed" }

& $apksigner verify --print-certs $finalApk
if ($LASTEXITCODE -ne 0) { throw "apksigner verify failed" }

if ($workRoot -ne $srcRoot) {
    $outBuildDir = Join-Path $srcRoot "build"
    New-Item -ItemType Directory -Force -Path $outBuildDir | Out-Null
    $destApk = Join-Path $outBuildDir "hotspot-proxy.apk"
    Copy-Item $finalApk $destApk -Force
    if (-not (Test-Path (Join-Path $srcRoot "debug.keystore")) -and (Test-Path $keystore)) {
        Copy-Item $keystore (Join-Path $srcRoot "debug.keystore") -Force
    }
    $finalApk = $destApk
}

Write-Host "SUCCESS: $finalApk" -ForegroundColor Green
