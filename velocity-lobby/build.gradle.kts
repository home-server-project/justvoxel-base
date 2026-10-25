plugins { java }

group = "org.homeserverproject.justvoxel"
version = "1.0.0"

providers.gradleProperty("buildOutputDir").orNull?.let {
    layout.buildDirectory.set(file(it))
}

java { toolchain { languageVersion.set(JavaLanguageVersion.of(25)) } }
tasks.withType<JavaCompile>().configureEach {
    options.release.set(25)
    options.encoding = "UTF-8"
}

repositories {
    mavenCentral()
    maven("https://repo.papermc.io/repository/maven-public/")
    maven("https://repo.opencollab.dev/main/")
}

val limboApiJar = providers.gradleProperty("limboApiJar")
val velocityVersion = providers.gradleProperty("velocityVersion").orNull
    ?.takeIf { it.isNotBlank() }
    ?: throw GradleException("Missing -PvelocityVersion: use the resolved stable Velocity version")
val velocityServerJar = providers.gradleProperty("velocityServerJar").orNull
    ?.takeIf { it.isNotBlank() }
    ?: throw GradleException("Missing -PvelocityServerJar: supply the verified official Velocity server JAR")
require(file(velocityServerJar).isFile) { "velocityServerJar must be an existing verified server JAR" }
dependencies {
    // The downloaded, SHA-512 verified stable Modrinth JAR; supplied at runtime.
    compileOnly(files(limboApiJar))
    compileOnly("com.velocitypowered:velocity-api:$velocityVersion")
    annotationProcessor("com.velocitypowered:velocity-api:$velocityVersion")
    // Only the LimboAPI/Floodgate PluginMessagePacket bridge needs server internals.
    compileOnly(files(velocityServerJar))
    compileOnly("org.geysermc.floodgate:api:2.2.5-SNAPSHOT")
    compileOnly("io.netty:netty-buffer:4.2.18.Final")
    testImplementation("org.junit.jupiter:junit-jupiter:5.12.2")
    testRuntimeOnly("org.junit.platform:junit-platform-launcher")
}

tasks.jar {
    archiveFileName.set("justvoxel-lobby.jar")
    isPreserveFileTimestamps = false
    isReproducibleFileOrder = true
}
tasks.test { useJUnitPlatform() }
