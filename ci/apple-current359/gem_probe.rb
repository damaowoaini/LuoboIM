# Fixed Ruby 3.3.12/CocoaPods 1.17 observation only. No installation or user gems.
bootstrap_features = $LOADED_FEATURES.dup.freeze
require 'json'
require 'cocoapods'
abort 'fixed Ruby/CocoaPods identity' unless RUBY_VERSION == '3.3.12' &&
  Pod::VERSION == '1.17.0' &&
  Gem.loaded_specs.fetch('cocoapods').version.to_s == '1.17.0' &&
  Gem.loaded_specs.fetch('cocoapods-core').version.to_s == '1.17.0'

def observed_path(path)
  kind = if File.file?(path)
    'file'
  elsif File.directory?(path)
    'directory'
  elsif File.exist?(path)
    'other'
  else
    'missing'
  end
  { 'lexical' => path, 'canonical' => kind == 'missing' ? nil : File.realpath(path), 'kind' => kind }
end

specs = Gem.loaded_specs.values.map do |spec|
  required = spec.full_require_paths
  {
    'name' => spec.name, 'version' => spec.version.to_s, 'full_name' => spec.full_name,
    'default' => spec.default_gem?, 'loaded_from' => spec.loaded_from,
    'full_gem_path' => spec.full_gem_path, 'require_paths' => spec.raw_require_paths,
    'full_require_paths' => required, 'files' => spec.files,
    'path_mappings' => {
      'spec' => observed_path(spec.loaded_from), 'gem' => observed_path(spec.full_gem_path),
      'requires' => required.map { |path| observed_path(path) }
    }
  }
end
config_keys = %w[prefix bindir libdir rubylibdir rubyarchdir ruby_version arch DLEXT ENABLE_SHARED LIBRUBY_SO sitedir sitelibdir sitearchdir vendordir vendorlibdir vendorarchdir]
observation = {
  'schema' => 2, 'ruby' => RUBY_VERSION, 'rubygems' => Gem::VERSION,
  'ruby_binary' => RbConfig.ruby,
  'config' => config_keys.to_h { |key| [key, RbConfig::CONFIG.fetch(key)] },
  'config_path_mappings' => %w[sitedir sitelibdir sitearchdir vendordir vendorlibdir vendorarchdir].to_h do |key|
    [key, observed_path(RbConfig::CONFIG.fetch(key))]
  end,
  'gem_dir' => Gem.dir, 'gem_path' => Gem.path, 'default_dir' => Gem.default_dir,
  'default_specifications_dir' => Gem.default_specifications_dir,
  'load_path' => $LOAD_PATH.dup, 'gems' => specs
}
# Take the final feature snapshot after spec/config introspection. A feature can
# be an MRI-provided marker, rather than a disk filename. Python validates the
# finite official marker list, bootstrap membership and Ruby/runtime binding.
observation['features'] = $LOADED_FEATURES.map do |feature|
  candidates = if feature.start_with?('/')
    [feature]
  else
    observation['load_path'].map { |entry| File.expand_path(feature, entry) }
  end
  paths = candidates.select { |path| File.file?(path) }.map { |path| File.realpath(path) }.uniq
  { 'feature' => feature, 'bootstrap' => bootstrap_features.include?(feature), 'paths' => paths }
end
puts JSON.generate(observation)
